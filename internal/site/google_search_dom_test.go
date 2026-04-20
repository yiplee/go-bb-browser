package site

import "testing"

func TestParseGoogleSearchArgs(t *testing.T) {
	q, n, err := ParseGoogleSearchArgs([]byte(`{"query":"bitcoin","count":"5","arg1":"bitcoin"}`))
	if err != nil || q != "bitcoin" || n != 5 {
		t.Fatalf("got %q %d %v", q, n, err)
	}
	q2, n2, err := ParseGoogleSearchArgs([]byte(`{"query":"x"}`))
	if err != nil || q2 != "x" || n2 != 10 {
		t.Fatalf("defaults %+v", []any{q2, n2, err})
	}
	if _, _, err := ParseGoogleSearchArgs([]byte(`{"arg1":"only"}`)); err == nil {
		t.Fatal("expected error")
	}
}
