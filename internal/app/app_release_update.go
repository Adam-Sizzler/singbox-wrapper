//go:build windows

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

// appUpdateHTTPClient переиспользуется для проверки обновлений — не создаём Transport каждый раз.
var appUpdateHTTPClient = &http.Client{Timeout: 15 * time.Second}

// appUpdateDownloadTimeout — увеличенный таймаут для скачивания бинарника (до 300 МБ).
const appUpdateDownloadTimeout = 5 * time.Minute

const (
	appReleaseLatestAPIURL       = "https://api.github.com/repos/Adam-Sizzler/singbox-wrapper/releases/latest"
	appReleaseBinaryAssetName    = "singbox-wrapper.exe"
	appReleaseCheckInterval      = 12 * time.Hour
	appReleaseCheckErrorInterval = 10 * time.Minute
)

type versionTag struct {
	parts      []int
	prerelease string
}

type appReleaseInfo struct {
	displayTag string
	releaseURL string
	assetURL   string
}

func (a *App) appUpdateSnapshot() (bool, string, string) {
	currentTag := currentAppReleaseTag()
	if currentTag == "" {
		return false, "", ""
	}

	now := time.Now()
	a.appUpdateMu.Lock()
	shouldCheck := !a.appUpdateChecking && (a.appUpdateCheckedAt.IsZero() || !now.Before(a.appUpdateNextCheckAt))
	if shouldCheck {
		a.appUpdateChecking = true
	}
	available := a.appUpdateAvailable
	latestTag := a.appLatestReleaseTag
	latestURL := a.appLatestReleaseURL
	a.appUpdateMu.Unlock()

	if shouldCheck {
		go a.refreshAppUpdateStatus(currentTag)
	}

	return available, latestTag, latestURL
}

func (a *App) refreshAppUpdateStatus(currentTag string) {
	latestTag, latestURL, err := fetchLatestAppRelease()
	now := time.Now()

	a.appUpdateMu.Lock()
	defer a.appUpdateMu.Unlock()
	a.appUpdateChecking = false
	a.appUpdateCheckedAt = now

	if err != nil {
		a.appUpdateNextCheckAt = now.Add(appReleaseCheckErrorInterval)
		return
	}

	a.appUpdateNextCheckAt = now.Add(appReleaseCheckInterval)
	a.appLatestReleaseTag = latestTag
	a.appLatestReleaseURL = latestURL
	a.appUpdateAvailable = isVersionTagNewer(currentTag, latestTag)
}

func fetchLatestAppRelease() (string, string, error) {
	info, err := fetchLatestAppReleaseInfo()
	if err != nil {
		return "", "", err
	}
	return info.displayTag, info.releaseURL, nil
}

func fetchLatestAppReleaseInfo() (appReleaseInfo, error) {
	req, err := http.NewRequest(http.MethodGet, appReleaseLatestAPIURL, nil)
	if err != nil {
		return appReleaseInfo{}, err
	}
	req.Header.Set("User-Agent", appUserAgent())

	resp, err := appUpdateHTTPClient.Do(req)
	if err != nil {
		return appReleaseInfo{}, fmt.Errorf("github недоступен: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return appReleaseInfo{}, fmt.Errorf("github вернул HTTP %d", resp.StatusCode)
	}

	var body struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return appReleaseInfo{}, fmt.Errorf("не удалось распарсить ответ GitHub: %w", err)
	}

	tagRaw := strings.TrimSpace(body.TagName)
	parsed, ok := parseVersionTag(tagRaw)
	if !ok {
		return appReleaseInfo{}, fmt.Errorf("получен некорректный tag_name: %q", body.TagName)
	}

	displayTag := strings.TrimSpace(tagRaw)
	if displayTag == "" || strings.EqualFold(displayTag, parsed.normalizedString()) {
		displayTag = "v" + parsed.normalizedString()
	}

	releaseURL := strings.TrimSpace(body.HTMLURL)
	if releaseURL == "" {
		releaseURL = appReleaseBaseURL + neturl.PathEscape(displayTag)
	}

	assetURL := ""
	for _, asset := range body.Assets {
		if strings.EqualFold(strings.TrimSpace(asset.Name), appReleaseBinaryAssetName) {
			assetURL = strings.TrimSpace(asset.BrowserDownloadURL)
			break
		}
	}

	if assetURL == "" {
		for _, asset := range body.Assets {
			name := strings.ToLower(strings.TrimSpace(asset.Name))
			if strings.HasSuffix(name, ".exe") {
				assetURL = strings.TrimSpace(asset.BrowserDownloadURL)
				break
			}
		}
	}

	return appReleaseInfo{
		displayTag: displayTag,
		releaseURL: releaseURL,
		assetURL:   assetURL,
	}, nil
}

func (a *App) updateApplicationAction() error {
	return a.withRunningAction(func() error {
		currentTag := currentAppReleaseTag()
		if currentTag == "" {
			return errors.New("версия текущего приложения неизвестна, обновление недоступно")
		}

		latest, err := fetchLatestAppReleaseInfo()
		if err != nil {
			return err
		}
		if latest.assetURL == "" {
			return errors.New("в последнем релизе не найден исполняемый файл приложения")
		}
		if !isVersionTagNewer(currentTag, latest.displayTag) {
			return fmt.Errorf("обновление не требуется: установлена %s, последняя %s", currentTag, latest.displayTag)
		}

		currentExe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("не удалось определить путь текущего приложения: %w", err)
		}
		if real, realErr := filepath.EvalSymlinks(currentExe); realErr == nil && real != "" {
			currentExe = real
		}

		nextExe := currentExe + ".update.new"
		_ = os.Remove(nextExe)

		a.log("Скачивание обновления приложения: %s", latest.displayTag)
		if err := a.downloadAppUpdate(latest.assetURL, nextExe); err != nil {
			return fmt.Errorf("не удалось скачать обновление приложения: %w", err)
		}

		if err := launchSelfUpdateScript(currentExe, nextExe); err != nil {
			_ = os.Remove(nextExe)
			return err
		}

		a.log("Обновление приложения запущено: %s", latest.displayTag)
		a.closeForSelfUpdate()
		return nil
	})
}

func launchSelfUpdateScript(targetExe, sourceExe string) error {
	target := strings.TrimSpace(targetExe)
	source := strings.TrimSpace(sourceExe)
	if target == "" || source == "" {
		return errors.New("неверные параметры self-update")
	}

	scriptPath, err := writeSelfUpdateScript()
	if err != nil {
		return fmt.Errorf("не удалось подготовить update-script: %w", err)
	}

	launcherPath, err := writeSelfUpdateLauncherVBScript(scriptPath, source, target)
	if err != nil {
		_ = os.Remove(scriptPath)
		return fmt.Errorf("не удалось подготовить update-launcher: %w", err)
	}

	cmd := exec.Command("wscript.exe", "//nologo", launcherPath)
	cmd.Dir = filepath.Dir(target)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow | createNewProcessGroup,
		HideWindow:    true,
	}
	if err := cmd.Start(); err != nil {
		_ = os.Remove(scriptPath)
		_ = os.Remove(launcherPath)
		return fmt.Errorf("не удалось запустить update-script: %w", err)
	}
	return nil
}

func writeSelfUpdateLauncherVBScript(scriptPath, source, target string) (string, error) {
	launcherPath := filepath.Join(os.TempDir(), fmt.Sprintf("singbox-wrapper-self-update-launcher-%d.vbs", time.Now().UnixNano()))

	cmdLine := fmt.Sprintf(`cmd.exe /C ""%s" "%s" "%s""`, scriptPath, source, target)
	// Экранируем " для VBS и % для cmd.exe внутри VBS shell.Run
	escapedCmdLine := strings.ReplaceAll(cmdLine, `"`, `""`)
	escapedCmdLine = strings.ReplaceAll(escapedCmdLine, "%", "%%")

	launcher := strings.Join([]string{
		"On Error Resume Next",
		"Dim shell",
		`Set shell = CreateObject("WScript.Shell")`,
		fmt.Sprintf(`shell.Run "%s", 0, False`, escapedCmdLine),
		`CreateObject("Scripting.FileSystemObject").DeleteFile WScript.ScriptFullName, True`,
	}, "\r\n")

	if err := os.WriteFile(launcherPath, []byte(launcher), 0600); err != nil {
		return "", err
	}
	return launcherPath, nil
}

func writeSelfUpdateScript() (string, error) {
	scriptPath := filepath.Join(os.TempDir(), fmt.Sprintf("singbox-wrapper-self-update-%d.cmd", time.Now().UnixNano()))
	script := strings.Join([]string{
		"@echo off",
		"setlocal EnableExtensions",
		"set \"SRC=%~1\"",
		"set \"DST=%~2\"",
		"if \"%SRC%\"==\"\" exit /b 1",
		"if \"%DST%\"==\"\" exit /b 1",
		"set \"OLD=%DST%.update.old\"",
		"for /L %%I in (1,1,120) do (",
		"  move /Y \"%DST%\" \"%OLD%\" >nul 2>&1",
		"  if not errorlevel 1 goto replace",
		"  if not exist \"%DST%\" goto replace",
		"  >nul ping -n 2 127.0.0.1",
		")",
		"exit /b 1",
		":replace",
		"move /Y \"%SRC%\" \"%DST%\" >nul 2>&1",
		"if errorlevel 1 (",
		"  if exist \"%OLD%\" move /Y \"%OLD%\" \"%DST%\" >nul 2>&1",
		"  exit /b 1",
		")",
		"if exist \"%OLD%\" del /f /q \"%OLD%\" >nul 2>&1",
		"start \"\" /B \"%DST%\"",
		"del /f /q \"%~f0\" >nul 2>&1",
		"exit /b 0",
	}, "\r\n")

	if err := os.WriteFile(scriptPath, []byte(script), 0600); err != nil {
		return "", err
	}
	return scriptPath, nil
}

// downloadAppUpdate скачивает бинарник обновления и репортует прогресс
// через CSS-переменную --update-progress в webview (визуальный прогресс-бар на кнопке).
func (a *App) downloadAppUpdate(url, target string) error {
	// Устанавливаем прогресс в 0 и добавляем класс на body для активации CSS прогрессбара.
	atomic.StoreInt32(&a.appUpdateProgressVal, 0)
	a.setUpdateProgress(0)

	defer func() {
		// Сбрасываем прогресс после завершения (успех или ошибка).
		atomic.StoreInt32(&a.appUpdateProgressVal, -1)
		a.clearUpdateProgress()
	}()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", appUserAgent())

	client := &http.Client{Timeout: appUpdateDownloadTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	total := resp.ContentLength // -1 если сервер не вернул Content-Length

	tmpPath := target + ".tmp"
	file, err := os.Create(tmpPath)
	if err != nil {
		return err
	}

	var downloaded int64
	lastLoggedPct := -1
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := file.Write(buf[:n]); writeErr != nil {
				file.Close()
				_ = os.Remove(tmpPath)
				return writeErr
			}
			downloaded += int64(n)
			if total > 0 {
				pct := int(downloaded * 100 / total)
				if pct > 100 {
					pct = 100
				}
				atomic.StoreInt32(&a.appUpdateProgressVal, int32(pct))
				// Логируем и обновляем UI каждые 10%
				if pct >= lastLoggedPct+10 {
					lastLoggedPct = pct
					a.log("Скачивание обновления: %.1f МБ / %.1f МБ (%d%%)",
						float64(downloaded)/(1024*1024),
						float64(total)/(1024*1024),
						pct)
					a.setUpdateProgress(pct)
				}
			} else {
				// Неизвестный размер — логируем каждые 5 МБ
				const logEvery = 5 * 1024 * 1024
				if downloaded/logEvery > (downloaded-int64(n))/logEvery {
					a.log("Скачивание обновления: %.1f МБ...", float64(downloaded)/(1024*1024))
				}
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			file.Close()
			_ = os.Remove(tmpPath)
			return readErr
		}
	}

	if err := file.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	// Финальный прогресс 100%
	atomic.StoreInt32(&a.appUpdateProgressVal, 100)
	a.setUpdateProgress(100)
	return nil
}

// setUpdateProgress устанавливает CSS-переменную --update-progress в webview.
// Добавляет класс app-updating на body при первом вызове.
func (a *App) setUpdateProgress(pct int) {
	if a.web == nil {
		return
	}
	// Используем Dispatch чтобы Eval выполнился на потоке webview — безопасно
	// вызывать из любой горутины (загрузка файла, etc.)
	js := fmt.Sprintf(
		`(function(){`+
			`document.documentElement.style.setProperty('--update-progress','%d%%');`+
			`document.body.classList.add('app-updating');`+
			`})();`,
		pct,
	)
	a.web.Dispatch(func() { _ = a.web.Eval(js) })
}

// clearUpdateProgress сбрасывает прогресс-бар и убирает класс app-updating.
func (a *App) clearUpdateProgress() {
	if a.web == nil {
		return
	}
	a.web.Dispatch(func() {
		_ = a.web.Eval(
			`(function(){` +
				`document.documentElement.style.removeProperty('--update-progress');` +
				`document.body.classList.remove('app-updating');` +
				`})();`,
		)
	})
}

func (a *App) closeForSelfUpdate() {
	go func() {
		time.Sleep(1000 * time.Millisecond) // Даём wscript.exe время запуститься и прочитать .cmd файл
		a.requestMainWindowClose()
	}()
}

func isVersionTagNewer(currentTag, latestTag string) bool {
	current, okCurrent := parseVersionTag(currentTag)
	latest, okLatest := parseVersionTag(latestTag)
	if !okCurrent || !okLatest {
		return false
	}

	maxLen := len(current.parts)
	if len(latest.parts) > maxLen {
		maxLen = len(latest.parts)
	}
	for i := 0; i < maxLen; i++ {
		var currentPart int
		if i < len(current.parts) {
			currentPart = current.parts[i]
		}
		var latestPart int
		if i < len(latest.parts) {
			latestPart = latest.parts[i]
		}
		if latestPart != currentPart {
			return latestPart > currentPart
		}
	}

	if latest.prerelease == "" && current.prerelease != "" {
		return true
	}
	if latest.prerelease != "" && current.prerelease == "" {
		return false
	}
	if latest.prerelease == current.prerelease {
		return false
	}

	return strings.Compare(latest.prerelease, current.prerelease) > 0
}

func parseVersionTag(raw string) (versionTag, bool) {
	tag := strings.TrimSpace(raw)
	tag = strings.TrimPrefix(tag, "v")
	tag = strings.TrimPrefix(tag, "V")
	if tag == "" {
		return versionTag{}, false
	}

	if buildIdx := strings.Index(tag, "+"); buildIdx >= 0 {
		tag = tag[:buildIdx]
	}

	parsed := versionTag{}
	if preIdx := strings.Index(tag, "-"); preIdx >= 0 {
		parsed.prerelease = strings.TrimSpace(tag[preIdx+1:])
		tag = tag[:preIdx]
	}

	core := strings.Split(tag, ".")
	if len(core) < 3 {
		return versionTag{}, false
	}

	parts := make([]int, 0, len(core))
	for _, part := range core {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || value < 0 {
			return versionTag{}, false
		}
		parts = append(parts, value)
	}
	parsed.parts = parts
	return parsed, true
}

func (v versionTag) normalizedString() string {
	if len(v.parts) == 0 {
		return ""
	}
	partStrings := make([]string, 0, len(v.parts))
	for _, part := range v.parts {
		partStrings = append(partStrings, strconv.Itoa(part))
	}
	base := strings.Join(partStrings, ".")
	if v.prerelease == "" {
		return base
	}
	return base + "-" + v.prerelease
}
