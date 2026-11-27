package location

import "slices"

// countryCodes lists the ISO 3166-1 officially assigned alpha-2 country codes
// (249) and XK, the code Kosovo uses in practice, sorted. The 249 come from the
// ISO3166-1-Alpha-2 column of
// https://raw.githubusercontent.com/datasets/country-codes/main/data/country-codes.csv
// (the Open Knowledge Foundation's country-codes dataset). Reserved and withdrawn
// codes (AN, UK, SU, AC, EU, ...) are not countries here.
var countryCodes = []string{
	"AD", "AE", "AF", "AG", "AI", "AL", "AM", "AO", "AQ", "AR",
	"AS", "AT", "AU", "AW", "AX", "AZ", "BA", "BB", "BD", "BE",
	"BF", "BG", "BH", "BI", "BJ", "BL", "BM", "BN", "BO", "BQ",
	"BR", "BS", "BT", "BV", "BW", "BY", "BZ", "CA", "CC", "CD",
	"CF", "CG", "CH", "CI", "CK", "CL", "CM", "CN", "CO", "CR",
	"CU", "CV", "CW", "CX", "CY", "CZ", "DE", "DJ", "DK", "DM",
	"DO", "DZ", "EC", "EE", "EG", "EH", "ER", "ES", "ET", "FI",
	"FJ", "FK", "FM", "FO", "FR", "GA", "GB", "GD", "GE", "GF",
	"GG", "GH", "GI", "GL", "GM", "GN", "GP", "GQ", "GR", "GS",
	"GT", "GU", "GW", "GY", "HK", "HM", "HN", "HR", "HT", "HU",
	"ID", "IE", "IL", "IM", "IN", "IO", "IQ", "IR", "IS", "IT",
	"JE", "JM", "JO", "JP", "KE", "KG", "KH", "KI", "KM", "KN",
	"KP", "KR", "KW", "KY", "KZ", "LA", "LB", "LC", "LI", "LK",
	"LR", "LS", "LT", "LU", "LV", "LY", "MA", "MC", "MD", "ME",
	"MF", "MG", "MH", "MK", "ML", "MM", "MN", "MO", "MP", "MQ",
	"MR", "MS", "MT", "MU", "MV", "MW", "MX", "MY", "MZ", "NA",
	"NC", "NE", "NF", "NG", "NI", "NL", "NO", "NP", "NR", "NU",
	"NZ", "OM", "PA", "PE", "PF", "PG", "PH", "PK", "PL", "PM",
	"PN", "PR", "PS", "PT", "PW", "PY", "QA", "RE", "RO", "RS",
	"RU", "RW", "SA", "SB", "SC", "SD", "SE", "SG", "SH", "SI",
	"SJ", "SK", "SL", "SM", "SN", "SO", "SR", "SS", "ST", "SV",
	"SX", "SY", "SZ", "TC", "TD", "TF", "TG", "TH", "TJ", "TK",
	"TL", "TM", "TN", "TO", "TR", "TT", "TV", "TW", "TZ", "UA",
	"UG", "UM", "US", "UY", "UZ", "VA", "VC", "VE", "VG", "VI",
	"VN", "VU", "WF", "WS", "XK", "YE", "YT", "ZA", "ZM", "ZW",
}

func isCountryCode(code string) bool {
	_, found := slices.BinarySearch(countryCodes, code)
	return found
}

// CountryCodes returns a copy of the sorted list of country codes a declaration may name.
func CountryCodes() []string { return slices.Clone(countryCodes) }
