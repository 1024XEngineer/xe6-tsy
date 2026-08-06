package asr

import "strings"

// NormalizeLanguage maps provider short tags (zh/en) and common aliases onto the
// BCP-47 codes used by language-config pairs (zh-CN/en-US).
func NormalizeLanguage(code string) string {
	code = strings.TrimSpace(code)
	if code == "" {
		return ""
	}
	code = strings.ReplaceAll(code, "_", "-")
	primary := code
	region := ""
	if index := strings.IndexByte(code, '-'); index > 0 {
		primary = code[:index]
		region = code[index+1:]
	}
	switch strings.ToLower(primary) {
	case "zh", "cmn":
		switch strings.ToUpper(region) {
		case "TW":
			return "zh-TW"
		case "HK":
			return "zh-HK"
		default:
			return "zh-CN"
		}
	case "yue":
		return "zh-HK"
	case "en":
		if strings.EqualFold(region, "GB") {
			return "en-GB"
		}
		return "en-US"
	}
	if region != "" {
		return strings.ToLower(primary) + "-" + strings.ToUpper(region)
	}
	return strings.ToLower(primary)
}
