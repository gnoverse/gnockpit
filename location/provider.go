package location

import (
	"strings"
	"unicode"
)

// OnPremise is the provider of a validator that runs on its operator's own hardware.
const OnPremise = "on-premise"

// MaxProvider is the longest provider name, in runes.
const MaxProvider = 64

// ProviderKey reduces a provider name to what identifies it: its letters and
// numbers, lowercased. Case, spaces and punctuation do not tell two spellings apart.
func ProviderKey(name string) string {
	var key strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			key.WriteRune(unicode.ToLower(r))
		}
	}
	return key.String()
}

// ListedProvider returns the first of listed, in its order, that has name's key,
// so one provider keeps one spelling. A name nothing listed matches is returned
// as written, and "" (no provider) stays "".
func ListedProvider(name string, listed []string) string {
	if name == "" {
		return ""
	}
	key := ProviderKey(name)
	for _, l := range listed {
		if ProviderKey(l) == key {
			return l
		}
	}
	return name
}
