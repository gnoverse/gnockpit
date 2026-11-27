package location

import (
	"slices"
	"testing"

	"golang.org/x/text/language"
)

func TestCountryCodes(t *testing.T) {
	codes := CountryCodes()
	if len(codes) != 250 {
		t.Errorf("len(codes) = %d, want 250", len(codes))
	}
	if !slices.IsSorted(codes) {
		t.Error("codes is not sorted")
	}
	if len(slices.Compact(slices.Clone(codes))) != len(codes) {
		t.Error("codes holds a duplicate")
	}
	for _, c := range codes {
		r, err := language.ParseRegion(c)
		if err != nil || r.String() != c || !r.IsCountry() {
			t.Errorf("codes holds %q, which x/text does not read as a country", c)
		}
	}
}

func TestCountryCodesReturnsACopy(t *testing.T) {
	CountryCodes()[0] = "ZZ"
	if ValidCountry("ZZ") || CountryCodes()[0] == "ZZ" {
		t.Error("changing the returned list changed the list")
	}
}
