//go:build windows

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var (
	kernel32DLL           = syscall.NewLazyDLL("kernel32.dll")
	procAttachConsole     = kernel32DLL.NewProc("AttachConsole")
	procFreeConsole       = kernel32DLL.NewProc("FreeConsole")
	procGenerateCtrlEvent = kernel32DLL.NewProc("GenerateConsoleCtrlEvent")
	procSetCtrlHandler    = kernel32DLL.NewProc("SetConsoleCtrlHandler")
)

const (
	uiConfigActionTimeout = 5 * time.Second
	singBoxCheckTimeout   = 12 * time.Second
)

func (a *App) isProcessRunning() bool {
	a.procMu.Lock()
	defer a.procMu.Unlock()
	return a.proc != nil && a.proc.Process != nil
}

func (a *App) setCoreDesiredRunning(v bool) {
	a.coreDesiredMu.Lock()
	a.coreDesiredRunning = v
	a.coreDesiredMu.Unlock()
}

func (a *App) coreDesiredRunningSnapshot() bool {
	a.coreDesiredMu.Lock()
	defer a.coreDesiredMu.Unlock()
	return a.coreDesiredRunning
}

func (a *App) processUptimeSeconds() int64 {
	a.procMu.Lock()
	defer a.procMu.Unlock()
	if a.proc == nil || a.proc.Process == nil || a.procStartedAt.IsZero() {
		return 0
	}
	seconds := int64(time.Since(a.procStartedAt).Seconds())
	if seconds < 0 {
		return 0
	}
	return seconds
}

func (a *App) toggleStartStop() error {
	return a.withRunningAction(func() error {
		if a.isProcessRunning() {
			a.setCoreDesiredRunning(false)
			a.stopProcess()
			return nil
		}
		a.setCoreDesiredRunning(true)
		if err := a.startPipeline(); err != nil {
			a.setCoreDesiredRunning(false)
			return err
		}
		return nil
	})
}

func (a *App) checkConfigAction() error {
	return a.withRunningAction(func() error {
		cfg := a.getConfigSnapshot()
		if err := validateConfig(cfg); err != nil {
			return err
		}

		active := activeProfileFromConfig(cfg)
		profileName := strings.TrimSpace(active.Name)
		if profileName == "" {
			profileName = "profile-1"
		}
		runtimeCfgPath := a.runtimeConfigPathForProfile(profileName)
		runtimeCfgFile := filepath.Base(runtimeCfgPath)

		resolvedConfigURL, _, _, err := resolveSubscriptionInput(active.URL)
		if err != nil {
			return err
		}

		resolvedVersion, err := resolveVersion(active.Version)
		if err != nil {
			return fmt.Errorf("не удалось определить версию sing-box: %w", err)
		}
		if err := a.ensureSingBox(resolvedVersion); err != nil {
			return err
		}

		if strings.TrimSpace(resolvedConfigURL) == "" {
			if err := a.ensureLocalRuntimeConfig(runtimeCfgPath); err != nil {
				return err
			}
			if err := validateRuntimeConfigWithSingBox(a.singBoxPath, runtimeCfgPath, singBoxCheckTimeout); err != nil {
				return err
			}
			a.log("Проверка конфигурации OK: локальный %s валиден для sing-box (профиль: %s)", runtimeCfgFile, profileName)
			return nil
		}

		if err := validateRemoteRuntimeConfigWithSingBox(resolvedConfigURL, uiConfigActionTimeout, cfg.AllowInsecure, a.singBoxPath, singBoxCheckTimeout); err != nil {
			return err
		}
		a.log("Проверка конфигурации OK: URL доступен и конфиг валиден для sing-box (профиль: %s)", profileName)
		return nil
	})
}

func (a *App) refreshConfigAction() error {
	return a.withRunningAction(func() error {
		cfg := a.getConfigSnapshot()
		if err := validateConfig(cfg); err != nil {
			return err
		}

		res, err := a.refreshActiveProfileRuntimeConfigFromURL(uiConfigActionTimeout)
		if err != nil {
			return err
		}

		if strings.TrimSpace(res.ResolvedConfigURL) == "" {
			if err := a.ensureLocalRuntimeConfig(res.RuntimeCfgPath); err != nil {
				return err
			}
			a.log("Конфигурация обновлена: подготовлен локальный %s (профиль: %s)", res.RuntimeCfgFile, res.ProfileName)
		} else {
			if res.Updated {
				a.log("Конфигурация обновлена: %s (профиль: %s)", res.RuntimeCfgFile, res.ProfileName)
				a.invalidateSelectorCache()
			} else {
				a.log("Конфигурация уже актуальна: %s (профиль: %s)", res.RuntimeCfgFile, res.ProfileName)
			}
		}

		if a.isProcessRunning() {
			a.log("Для применения обновлённого конфига перезапустите ядро")
		}
		return nil
	})
}

func (a *App) withRunningAction(fn func() error) error {
	a.runMu.Lock()
	if a.runningAction {
		a.runMu.Unlock()
		return errors.New("операция уже выполняется")
	}
	a.runningAction = true
	a.runMu.Unlock()
	defer func() {
		a.runMu.Lock()
		a.runningAction = false
		a.runMu.Unlock()
	}()
	return fn()
}

func (a *App) startPipeline() error {
	if !isRunningAsAdmin() {
		return errors.New("приложение запущено без прав администратора")
	}

	cfg := a.getConfigSnapshot()
	if err := validateConfig(cfg); err != nil {
		return err
	}
	active := activeProfileFromConfig(cfg)
	resolvedConfigURL, _, _, err := resolveSubscriptionInput(active.URL)
	if err != nil {
		return err
	}
	runtimeCfgPath := a.runtimeConfigPathForProfile(active.Name)
	runtimeCfgFile := filepath.Base(runtimeCfgPath)

	if active.Name != "" {
		a.log("Профиль: %s", active.Name)
	}
	if err := saveConfig(a.configPath, cfg); err != nil {
		return fmt.Errorf("не удалось сохранить %s: %w", configFileName, err)
	}
	a.log("Сохранён %s", configFileName)

	resolvedVersion, err := resolveVersion(active.Version)
	if err != nil {
		return fmt.Errorf("не удалось определить версию sing-box: %w", err)
	}
	if err := a.ensureSingBox(resolvedVersion); err != nil {
		return err
	}

	if strings.TrimSpace(resolvedConfigURL) == "" {
		if err := a.ensureLocalRuntimeConfig(runtimeCfgPath); err != nil {
			return err
		}
		a.log("URL не задан, использую локальный %s", runtimeCfgFile)
	} else {
		updated, fetchErr := a.refreshRuntimeConfigFromURL(resolvedConfigURL, runtimeCfgPath)
		if fetchErr != nil {
			// Подписка недоступна — логируем предупреждение и пробуем использовать
			// кэшированный конфиг. Если кэша тоже нет — прерываем запуск.
			a.log("WARN: не удалось обновить подписку: %v", fetchErr)
			if err := a.ensureLocalRuntimeConfig(runtimeCfgPath); err != nil {
				return fmt.Errorf("подписка недоступна и локальный %s не найден: %w", runtimeCfgFile, fetchErr)
			}
			a.log("Использую кэшированный %s (подписка была недоступна)", runtimeCfgFile)
		} else if updated {
			a.log("Скачан и обновлён %s", runtimeCfgFile)
		} else {
			a.log("%s уже актуален", runtimeCfgFile)
		}
	}

	clashSupported, err := singBoxSupportsClashAPI(a.singBoxPath)
	if err != nil {
		a.log("WARN: не удалось проверить поддержку with_clash_api: %v (использую clash api по умолчанию)", err)
		clashSupported = true
	}

	controllerAddr := ""
	controllerSecret := ""
	runCfgPath := runtimeCfgPath
	runCfgTmpPath := ""
	if clashSupported {
		controllerAddr, err = allocateLocalControllerAddr()
		if err != nil {
			return fmt.Errorf("не удалось выделить порт для clash api: %w", err)
		}
		controllerSecret, err = generateClashSecret()
		if err != nil {
			return fmt.Errorf("не удалось создать секрет clash api: %w", err)
		}
		runCfgPath, runCfgTmpPath, err = a.runtimeConfigWithClashAPI(runtimeCfgPath, controllerAddr, controllerSecret)
		if err != nil {
			return fmt.Errorf("не удалось включить clash api в %s: %w", runtimeCfgFile, err)
		}
	} else {
		a.log("WARN: установленный sing-box не поддерживает with_clash_api, live-переключение selector отключено")
	}

	a.stopProcess()
	if clashSupported {
		a.setClashSession(controllerAddr, controllerSecret, runCfgPath, runCfgTmpPath)
	} else {
		a.resetClashSession()
	}
	if err := a.startProcess(runCfgPath, normalizeSingboxEnv(cfg.SingboxEnv)); err != nil {
		a.resetClashSession()
		return err
	}

	a.log("sing-box запущен")
	a.setCoreDesiredRunning(true)
	if clashSupported {
		a.applySavedSelectorSelections(active)
	}
	return nil
}

func (a *App) ensureLocalRuntimeConfig(runtimeCfgPath string) error {
	a.runtimeCfgMu.Lock()
	defer a.runtimeCfgMu.Unlock()
	return ensureLocalRuntimeConfig(runtimeCfgPath)
}

func (a *App) runtimeConfigWithClashAPI(runtimeCfgPath, controller, secret string) (runCfgPath string, tmpPath string, err error) {
	a.runtimeCfgMu.Lock()
	defer a.runtimeCfgMu.Unlock()

	content, err := os.ReadFile(runtimeCfgPath)
	if err != nil {
		return "", "", err
	}

	tmpFile, err := os.CreateTemp(a.workDir, filepath.Base(runtimeCfgPath)+".run-*.json")
	if err != nil {
		return "", "", err
	}
	tmpPath = tmpFile.Name()
	if _, err := tmpFile.Write(content); err != nil {
		tmpFile.Close()
		removeRuntimeTempFile(tmpPath)
		return "", "", err
	}
	if err := tmpFile.Close(); err != nil {
		removeRuntimeTempFile(tmpPath)
		return "", "", err
	}

	if err := ensureRuntimeConfigHasClashAPI(tmpPath, controller, secret); err != nil {
		removeRuntimeTempFile(tmpPath)
		return "", "", err
	}
	return tmpPath, tmpPath, nil
}

func removeRuntimeTempFile(path string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return
	}
	_ = os.Remove(path)
}

func (a *App) refreshRuntimeConfigFromURL(url, runtimeCfgPath string) (bool, error) {
	return a.refreshRuntimeConfigFromURLWithTimeout(url, runtimeCfgPath, 0)
}

func (a *App) refreshRuntimeConfigFromURLWithTimeout(url, runtimeCfgPath string, timeout time.Duration) (bool, error) {
	cfg := a.getConfigSnapshot()
	a.runtimeCfgMu.Lock()
	defer a.runtimeCfgMu.Unlock()
	return downloadRuntimeConfigWithOptions(url, runtimeCfgPath, timeout, cfg.AllowInsecure)
}

func (a *App) ensureSingBox(targetVersion string) error {
	installedVersion, err := detectSingBoxVersion(a.singBoxPath)
	if err != nil {
		return fmt.Errorf("не удалось проверить установленную версию sing-box: %w", err)
	}
	if installedVersion == targetVersion {
		a.log("Найдена подходящая версия sing-box: %s", installedVersion)
		return nil
	}

	a.log("Требуется sing-box %s (текущая: %s)", targetVersion, emptyIf(installedVersion, "не найден"))
	if err := downloadAndInstallSingBox(targetVersion, a.singBoxPath); err != nil {
		return err
	}
	a.log("Установлен sing-box %s", targetVersion)
	return nil
}

func (a *App) startProcess(runtimeCfgPath string, envOverrides map[string]string) error {
	if _, err := os.Stat(a.singBoxPath); err != nil {
		return fmt.Errorf("не найден %s", singboxExeName)
	}
	if _, err := os.Stat(runtimeCfgPath); err != nil {
		return fmt.Errorf("не найден %s", filepath.Base(runtimeCfgPath))
	}

	cmd := exec.Command(a.singBoxPath, "run", "-c", runtimeCfgPath)
	cmd.Dir = a.workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNoWindow | createNewProcessGroup}
	if len(envOverrides) > 0 {
		env := os.Environ()
		for key, value := range envOverrides {
			env = append(env, key+"="+value)
		}
		cmd.Env = env
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan struct{})
	a.procMu.Lock()
	a.proc = cmd
	a.procStopRequested = false
	a.procWaitDone = done
	a.procStartedAt = time.Now()
	a.procMu.Unlock()

	go a.pipeLogs(stdout)
	go a.pipeLogs(stderr)

	go func(proc *exec.Cmd, waitDone chan struct{}) {
		err := proc.Wait()
		close(waitDone)

		a.procMu.Lock()
		wasStop := a.procStopRequested
		if a.proc == proc {
			a.proc = nil
			a.procStopRequested = false
			a.procWaitDone = nil
			a.procStartedAt = time.Time{}
		}
		a.procMu.Unlock()
		a.resetClashSession()

		if err != nil {
			if !wasStop {
				a.log("WARN: sing-box завершился с ошибкой: %v", err)
			}
			return
		}
		if !wasStop {
			a.log("sing-box завершился")
		}
	}(cmd, done)

	return nil
}

func (a *App) stopProcess() {
	a.procMu.Lock()
	proc := a.proc
	waitDone := a.procWaitDone
	if proc == nil || proc.Process == nil {
		a.procMu.Unlock()
		a.resetClashSession()
		return
	}
	a.procStopRequested = true
	pid := proc.Process.Pid
	a.procMu.Unlock()

	a.log("Остановка sing-box (pid=%d)", pid)

	graceful := tryGracefulProcessStop(pid, proc.Process)
	if graceful && waitDone != nil {
		if waitForProcessExit(waitDone, gracefulStopTimeout) {
			a.log("sing-box остановлен")
			return
		}
		a.log("WARN: таймаут мягкой остановки, применяю принудительное завершение")
	}

	if err := proc.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		a.log("WARN: не удалось завершить процесс: %v", err)
	}

	if waitDone != nil {
		_ = waitForProcessExit(waitDone, forceStopTimeout)
	}
	a.resetClashSession()
	a.log("sing-box остановлен")
}

func waitForProcessExit(done <-chan struct{}, timeout time.Duration) bool {
	if done == nil {
		return true
	}
	if timeout <= 0 {
		<-done
		return true
	}
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func tryGracefulProcessStop(pid int, proc *os.Process) bool {
	if pid <= 0 || proc == nil {
		return false
	}
	if err := sendCtrlBreakToProcessGroup(pid); err != nil {
		return false
	}
	return true
}

func sendCtrlBreakToProcessGroup(pid int) error {
	if pid <= 0 {
		return errors.New("invalid pid")
	}
	if err := kernel32DLL.Load(); err != nil {
		return err
	}
	if err := procAttachConsole.Find(); err != nil {
		return err
	}
	if err := procFreeConsole.Find(); err != nil {
		return err
	}
	if err := procGenerateCtrlEvent.Find(); err != nil {
		return err
	}
	if err := procSetCtrlHandler.Find(); err != nil {
		return err
	}

	_, _, _ = procFreeConsole.Call()
	if ret, _, callErr := procAttachConsole.Call(uintptr(pid)); ret == 0 {
		return normalizeWinProcErr("AttachConsole", callErr)
	}
	defer procFreeConsole.Call()

	if ret, _, callErr := procSetCtrlHandler.Call(0, 1); ret == 0 {
		return normalizeWinProcErr("SetConsoleCtrlHandler(add)", callErr)
	}
	defer procSetCtrlHandler.Call(0, 0)

	if ret, _, callErr := procGenerateCtrlEvent.Call(uintptr(ctrlBreakEvent), uintptr(pid)); ret == 0 {
		return normalizeWinProcErr("GenerateConsoleCtrlEvent", callErr)
	}

	time.Sleep(120 * time.Millisecond)
	return nil
}

func normalizeWinProcErr(api string, err error) error {
	if err == nil || errors.Is(err, syscall.Errno(0)) {
		return fmt.Errorf("%s failed", api)
	}
	return fmt.Errorf("%s: %w", api, err)
}

func emptyIf(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func commandWithTimeout(bin string, timeout time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: createNoWindow,
		HideWindow:    true,
	}
	return cmd.CombinedOutput()
}
