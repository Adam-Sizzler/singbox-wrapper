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
	majorStr := readRegistryString(`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "CurrentMajorVersionNumber")
	buildStr := readRegistryString(`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "CurrentBuildNumber")
	ubrStr := readRegistryString(`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "UBR")
	edition := readRegistryString(`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "EditionID")
	display := readRegistryString(`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "DisplayVersion")

	major := parseUint32(majorStr)
	build := parseUint32(buildStr)

	var osName string
	switch {
	case major >= 10 && build >= 22000:
		osName = "Windows 11"
	case major >= 10:
		osName = "Windows 10"
	default:
		osName = readRegistryString(`SOFTWARE\Microsoft\Windows NT\CurrentVersion`, "ProductName")
		if osName == "" {
			osName = "Windows"
		}
	}

	if friendly := editionFriendlyName(edition); friendly != "" {
		osName += " " + friendly
	}

	parts := []string{osName}

	if display != "" {
		parts = append(parts, display)
	}

	if buildStr != "" {
		b := buildStr
		if ubrStr != "" {
			b += "." + ubrStr
		}
		parts = append(parts, "build "+b)
	}

	return strings.Join(parts, " ")
}

func editionFriendlyName(editionID string) string {
	switch strings.ToLower(strings.TrimSpace(editionID)) {
	case "professional", "professionaln":
		return "Pro"
	case "professionalworkstation":
		return "Pro for Workstations"
	case "enterprise", "enterprisen":
		return "Enterprise"
	case "education", "educationn":
		return "Education"
	case "home", "homen", "core", "coren":
		return "Home"
	case "serverstandard":
		return "Server Standard"
	case "serverdatacenter":
		return "Server Datacenter"
	default:
		return editionID
	}
}

func parseUint32(s string) uint32 {
	s = strings.TrimSpace(s)
	var n uint32
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + uint32(c-'0')
	}
	return n
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
