//go:build windows

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows/registry"
)

// Пакетные переменные для системных DLL и proc — создаём один раз.
var (
	sysDLLKernel32 = syscall.NewLazyDLL("kernel32.dll")
	sysDLLUser32   = syscall.NewLazyDLL("user32.dll")
	sysDLLGdi32    = syscall.NewLazyDLL("gdi32.dll")
	sysDLLShell32  = syscall.NewLazyDLL("shell32.dll")

	procGetConsoleWindow = sysDLLKernel32.NewProc("GetConsoleWindow")
	procShowWindowSys    = sysDLLUser32.NewProc("ShowWindow")
	procGetDpiForSystem  = sysDLLUser32.NewProc("GetDpiForSystem")
	procGetDCSys         = sysDLLUser32.NewProc("GetDC")
	procReleaseDCSys     = sysDLLUser32.NewProc("ReleaseDC")
	procGetDeviceCaps    = sysDLLGdi32.NewProc("GetDeviceCaps")
	procIsUserAnAdmin    = sysDLLShell32.NewProc("IsUserAnAdmin")
	procShellExecuteW    = sysDLLShell32.NewProc("ShellExecuteW")
)

func hideConsoleWindow() {
	const swHide = 0
	hwnd, _, _ := procGetConsoleWindow.Call()
	if hwnd == 0 {
		return
	}
	_, _, _ = procShowWindowSys.Call(hwnd, uintptr(swHide))
}

func systemUIScale() float64 {
	dpi := systemDPI()
	if dpi < 96 {
		dpi = 96
	}
	scale := float64(dpi) / 96.0
	if scale < 1.0 {
		return 1.0
	}
	if scale > 3.0 {
		return 3.0
	}
	return scale
}

func systemDPI() int {
	if err := procGetDpiForSystem.Find(); err == nil {
		if dpi, _, _ := procGetDpiForSystem.Call(); dpi >= 96 && dpi <= 960 {
			return int(dpi)
		}
	}
	if hdc, _, _ := procGetDCSys.Call(0); hdc != 0 {
		const logPixelsX = 88
		dpi, _, _ := procGetDeviceCaps.Call(hdc, uintptr(logPixelsX))
		_, _, _ = procReleaseDCSys.Call(0, hdc)
		if dpi >= 96 && dpi <= 960 {
			return int(dpi)
		}
	}
	return 96
}

func isRunningAsAdmin() bool {
	ret, _, _ := procIsUserAnAdmin.Call()
	return ret != 0
}

func openURLInDefaultBrowser(rawURL string) error {
	target := strings.TrimSpace(rawURL)
	if target == "" {
		return fmt.Errorf("пустой URL")
	}
	verbPtr, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}
	targetPtr, err := syscall.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	ret, _, callErr := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verbPtr)),
		uintptr(unsafe.Pointer(targetPtr)),
		0,
		0,
		1,
	)
	if ret <= 32 {
		if callErr != syscall.Errno(0) {
			return fmt.Errorf("ShellExecuteW ret=%d: %w", ret, callErr)
		}
		return fmt.Errorf("ShellExecuteW ret=%d", ret)
	}
	return nil
}

func executableDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(exe)
	if err == nil {
		exe = real
	}
	return filepath.Dir(exe), nil
}

func ensureSingBoxProtocolRegistration() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	if real, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = real
	}

	basePath := `Software\Classes\sing-box`
	baseKey, _, err := registry.CreateKey(registry.CURRENT_USER, basePath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer baseKey.Close()

	if err := baseKey.SetStringValue("", "URL:sing-box Protocol"); err != nil {
		return err
	}
	if err := baseKey.SetStringValue("URL Protocol", ""); err != nil {
		return err
	}

	iconKey, _, err := registry.CreateKey(registry.CURRENT_USER, basePath+`\DefaultIcon`, registry.SET_VALUE)
	if err == nil {
		_ = iconKey.SetStringValue("", fmt.Sprintf(`"%s",0`, exePath))
		iconKey.Close()
	}

	cmdKey, _, err := registry.CreateKey(registry.CURRENT_USER, basePath+`\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer cmdKey.Close()

	command := fmt.Sprintf(`"%s" "%%1"`, exePath)
	return cmdKey.SetStringValue("", command)
}

func showError(title, message string) {
	if walk.MsgBox(nil, title, message, walk.MsgBoxIconError) != 0 {
		return
	}
	titlePtr, titleErr := syscall.UTF16PtrFromString(title)
	msgPtr, msgErr := syscall.UTF16PtrFromString(message)
	if titleErr != nil || msgErr != nil {
		return
	}
	_ = win.MessageBox(0, msgPtr, titlePtr, win.MB_OK|win.MB_ICONERROR|win.MB_TOPMOST)
}

// dpiScaleOnce гарантирует однократное вычисление DPI-компенсации.
var (
	dpiScaleOnce  sync.Once
	dpiScaleValue float64
)

// dpiCompensationFactor возвращает коэффициент уменьшения логического размера
// окна/UI при высоком DPI. Вычисляется один раз через sync.Once.
//
//	≤ 150% (dpr ≤ 1.5) → 1.0    (без изменений)
//	  200% (dpr = 2.0) → 0.80   (−20%)
//	линейная интерполяция
func dpiCompensationFactor() float64 {
	dpiScaleOnce.Do(func() {
		dpr := systemUIScale()
		const (
			threshold = 1.5
			full      = 2.0
			rate      = 0.20
		)
		if dpr <= threshold {
			dpiScaleValue = 1.0
			return
		}
		t := (dpr - threshold) / (full - threshold)
		if t > 1.0 {
			t = 1.0
		}
		dpiScaleValue = 1.0 - rate*t
	})
	return dpiScaleValue
}

// scaledSize применяет DPI-компенсацию к логическому размеру в пикселях.
func scaledSize(logical int, factor float64) int {
	return int(float64(logical) * factor)
}

// gclpHBRBACKGROUND — индекс WNDCLASSEX.hbrBackground в SetClassLongPtrW.
const gclpHBRBACKGROUND = -10

var (
	procCreateSolidBrush = sysDLLGdi32.NewProc("CreateSolidBrush")
)

// applyOwnerWindowBackground выставляет фоновый цвет класса окна чтобы
// при быстром ресайзе webview не было белых краёв за его границами.
func applyOwnerWindowBackground(hwnd win.HWND, dark bool) error {
	if hwnd == 0 {
		return fmt.Errorf("hwnd is 0")
	}
	var r, g, b uint32
	if dark {
		r, g, b = 0x19, 0x19, 0x27 // тёмно-синий/чёрный под тему приложения
	} else {
		r, g, b = 0xF5, 0xF5, 0xF5 // светло-серый под светлую тему
	}
	colorRef := uintptr((b << 16) | (g << 8) | r)
	hBrush, _, _ := procCreateSolidBrush.Call(colorRef)
	if hBrush == 0 {
		return fmt.Errorf("CreateSolidBrush failed")
	}
	// Заменяем кисть фона класса окна; старую кисть не удаляем — она системная.
	if err := procSetClassLongPtrW.Find(); err != nil {
		return err
	}
	idx := int32(gclpHBRBACKGROUND) // промежуточная переменная: константу нельзя напрямую привести к uintptr
	_, _, _ = procSetClassLongPtrW.Call(uintptr(hwnd), uintptr(idx), hBrush)
	return nil
}
