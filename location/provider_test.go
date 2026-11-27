package location

import "testing"

func TestProviderKey(t *testing.T) {
	for _, tc := range []struct{ name, want string }{
		{"OVH SAS", "ovhsas"},
		{"ovh sas", "ovhsas"},
		{"O.V.H. SAS", "ovhsas"},
		{"on-premise", "onpremise"},
		{"Ünïcode Ltd", "ünïcodeltd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProviderKey(tc.name); got != tc.want {
				t.Errorf("ProviderKey(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestListedProvider(t *testing.T) {
	listed := []string{"AWS", "Hetzner", "OVH SAS", OnPremise}
	for _, tc := range []struct{ name, want string }{
		{"ovh sas", "OVH SAS"},
		{"O.V.H. SAS", "OVH SAS"},
		{"On Premise", "on-premise"},
		{"Scaleway", "Scaleway"},
		{"", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ListedProvider(tc.name, listed); got != tc.want {
				t.Errorf("ListedProvider(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}
