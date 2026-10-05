package voice

import (
	"os"
	"strings"
)

type Language struct {
	Code string
	Name string
}

const LanguageAuto = "auto"

const LanguageDefault = "en"

var Languages = []Language{
	{Code: "ar", Name: "Arabic"},
	{Code: "cs", Name: "Czech"},
	{Code: "da", Name: "Danish"},
	{Code: "nl", Name: "Dutch"},
	{Code: "en", Name: "English"},
	{Code: "fil", Name: "Filipino"},
	{Code: "fr", Name: "French"},
	{Code: "de", Name: "German"},
	{Code: "hi", Name: "Hindi"},
	{Code: "id", Name: "Indonesian"},
	{Code: "it", Name: "Italian"},
	{Code: "ja", Name: "Japanese"},
	{Code: "ko", Name: "Korean"},
	{Code: "mk", Name: "Macedonian"},
	{Code: "ms", Name: "Malay"},
	{Code: "fa", Name: "Persian"},
	{Code: "pl", Name: "Polish"},
	{Code: "pt", Name: "Portuguese"},
	{Code: "ro", Name: "Romanian"},
	{Code: "ru", Name: "Russian"},
	{Code: "es", Name: "Spanish"},
	{Code: "sv", Name: "Swedish"},
	{Code: "th", Name: "Thai"},
	{Code: "tr", Name: "Turkish"},
	{Code: "vi", Name: "Vietnamese"},
}

func CanonicalizeLanguage(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return LanguageDefault
	}
	if strings.EqualFold(raw, LanguageAuto) {
		return LanguageAuto
	}
	if code := matchSupportedCode(raw); code != "" {
		return code
	}
	primary := primaryLanguageSubtag(raw)
	if code := matchSupportedCode(primary); code != "" {
		return code
	}
	if aliased := aliasToSupported(primary); aliased != "" {
		return aliased
	}
	return LanguageDefault
}

func LanguageForAPI(stored string) string {
	canonical := CanonicalizeLanguage(stored)
	if canonical == LanguageAuto {
		if sys := systemSTTLanguage(); sys != "" {
			return sys
		}
		return LanguageDefault
	}
	return canonical
}

func systemSTTLanguage() string {
	var loc string
	for _, v := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if val := strings.TrimSpace(os.Getenv(v)); val != "" {
			loc = val
			break
		}
	}
	if loc == "" || strings.EqualFold(loc, "C") || strings.EqualFold(loc, "POSIX") {
		return ""
	}
	primary := primaryLanguageSubtag(loc)
	if code := matchSupportedCode(primary); code != "" {
		return code
	}
	return aliasToSupported(primary)
}

func primaryLanguageSubtag(raw string) string {
	cut := strings.IndexAny(raw, "_-.")
	if cut < 0 {
		return strings.TrimSpace(raw)
	}
	return strings.TrimSpace(raw[:cut])
}

func matchSupportedCode(raw string) string {
	for _, lang := range Languages {
		if strings.EqualFold(raw, lang.Code) {
			return lang.Code
		}
	}
	return ""
}

func aliasToSupported(primary string) string {
	if strings.EqualFold(primary, "tl") {
		return "fil"
	}
	return ""
}
