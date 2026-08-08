package handlers

import "testing"

// SearchPersons matches the escaped prefix literally under ESCAPE '\'; the
// escaper must neutralize every LIKE metacharacter FoldPersonName preserves.
func TestLikePrefixEscaperNeutralizesWildcards(t *testing.T) {
	cases := map[string]string{
		`ada`:   `ada`,
		`100%`:  `100\%`,
		`a_b`:   `a\_b`,
		`back\`: `back\\`,
		`%_\`:   `\%\_\\`,
		``:      ``,
	}
	for input, want := range cases {
		if got := likePrefixEscaper.Replace(input); got != want {
			t.Errorf("escape(%q) = %q, want %q", input, got, want)
		}
	}
}
