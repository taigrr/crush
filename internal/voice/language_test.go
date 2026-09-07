package voice

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCatalogMatchesPublicDocs(t *testing.T) {
	t.Parallel()
	docs := []string{
		"ar", "cs", "da", "nl", "en", "fil", "fr", "de", "hi", "id", "it", "ja", "ko", "mk", "ms",
		"fa", "pl", "pt", "ro", "ru", "es", "sv", "th", "tr", "vi",
	}
	ours := make(map[string]struct{}, len(Languages))
	for _, lang := range Languages {
		ours[lang.Code] = struct{}{}
		require.NotEmpty(t, lang.Name)
		require.NotContains(t, lang.Code, "-")
	}
	require.Len(t, ours, len(docs))
	for _, code := range docs {
		_, ok := ours[code]
		require.True(t, ok, "missing catalog code %s", code)
	}
}

func TestCatalogSortedByEnglishName(t *testing.T) {
	t.Parallel()
	for i := 1; i < len(Languages); i++ {
		require.LessOrEqual(t, Languages[i-1].Name, Languages[i].Name)
	}
}

func TestCanonicalizeKnownAndUnknown(t *testing.T) {
	t.Parallel()
	require.Equal(t, "en", CanonicalizeLanguage(""))
	require.Equal(t, "en", CanonicalizeLanguage("  "))
	require.Equal(t, "en", CanonicalizeLanguage("en"))
	require.Equal(t, "es", CanonicalizeLanguage("ES"))
	require.Equal(t, "fr", CanonicalizeLanguage("  fr "))
	require.Equal(t, "auto", CanonicalizeLanguage("auto"))
	require.Equal(t, "auto", CanonicalizeLanguage("AUTO"))
	require.Equal(t, "en", CanonicalizeLanguage("en-US"))
	require.Equal(t, "pt", CanonicalizeLanguage("pt_BR.UTF-8"))
	require.Equal(t, "fil", CanonicalizeLanguage("tl"))
	require.Equal(t, "en", CanonicalizeLanguage("xx"))
}

func TestLanguageForAPINeverSendsAuto(t *testing.T) {
	t.Parallel()
	got := LanguageForAPI("auto")
	require.NotEqual(t, "auto", got)
	require.NotEmpty(t, matchSupportedCode(got))
}
