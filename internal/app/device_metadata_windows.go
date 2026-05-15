//go:build windows

package app

import "strings"

type appDeviceMetadataInfo struct {
	HWID        string
	Platform    string
	OSVersion   string
	DeviceModel string
	UserAgent   string
	AppVersion  string
}

func appDeviceMetadata() appDeviceMetadataInfo {
	return appDeviceMetadataInfo{
		HWID:        appHWID(),
		Platform:    "windows",
		OSVersion:   windowsOSVersion(),
		DeviceModel: windowsDeviceModel(),
		UserAgent:   appUserAgent(),
		AppVersion:  appVersionHeader(),
	}
}

func appVersionHeader() string {
	if tag := currentAppReleaseTag(); tag != "" {
		return tag
	}
	return "dev"
}

func windowsOSVersion() string {
	productName := readRegistryString(`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "ProductName")
	displayVersion := readRegistryString(`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "DisplayVersion")
	if displayVersion == "" {
		displayVersion = readRegistryString(`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "ReleaseId")
	}
	buildNumber := readRegistryString(`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "CurrentBuildNumber")
	ubr := readRegistryString(`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "UBR")

	parts := make([]string, 0, 3)
	if productName != "" {
		parts = append(parts, productName)
	}
	if displayVersion != "" {
		parts = append(parts, displayVersion)
	}
	if buildNumber != "" {
		if ubr != "" {
			buildNumber += "." + ubr
		}
		parts = append(parts, "build "+buildNumber)
	}
	if len(parts) == 0 {
		return "windows"
	}
	return strings.Join(parts, " ")
}

func windowsDeviceModel() string {
	manufacturer := firstMeaningfulDeviceValue(
		readRegistryString(`HARDWARE\DESCRIPTION\System\BIOS`, "SystemManufacturer"),
		readRegistryString(`HARDWARE\DESCRIPTION\System\BIOS`, "BaseBoardManufacturer"),
	)
	product := firstMeaningfulDeviceValue(
		readRegistryString(`HARDWARE\DESCRIPTION\System\BIOS`, "SystemProductName"),
		readRegistryString(`HARDWARE\DESCRIPTION\System\BIOS`, "BaseBoardProduct"),
	)

	switch {
	case manufacturer != "" && product != "" && !strings.EqualFold(manufacturer, product):
		return manufacturer + " " + product
	case product != "":
		return product
	case manufacturer != "":
		return manufacturer
	default:
		return "unknown"
	}
}

func firstMeaningfulDeviceValue(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || isPlaceholderDeviceValue(value) {
			continue
		}
		return value
	}
	return ""
}

func isPlaceholderDeviceValue(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "",
		"default string",
		"none",
		"not applicable",
		"not available",
		"o.e.m.",
		"oem",
		"system manufacturer",
		"system product name",
		"to be filled by o.e.m.",
		"to be filled by oem":
		return true
	default:
		return false
	}
}
