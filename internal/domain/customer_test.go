package domain

import "testing"

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{
		"+7 999 123-45-67":  "+79991234567",
		"8 (999) 123-45-67": "+79991234567",
		"9991234567":        "+79991234567",
		"+44 20 7946 0958":  "+442079460958",
		"12345":             "",
		"":                  "",
		"+0123456789012":    "",
	}
	for in, want := range cases {
		if got := NormalizePhone(in); got != want {
			t.Errorf("NormalizePhone(%q) = %q, want %q", in, got, want)
		}
	}
}
