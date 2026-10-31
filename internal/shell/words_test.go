package shell

import (
	"reflect"
	"testing"
)

func TestWords(t *testing.T) {
	for in, want := range map[string][]string{
		`make a b`:              {"make", "a", "b"},
		`echo "a b" 'c d' e\ f`: {"echo", "a b", "c d", "e f"},
		`  spaced   out  `:      {"spaced", "out"},
		`FOO="x y" make`:        {"FOO=x y", "make"},
		``:                      nil,
	} {
		if got := splitWords(in); !reflect.DeepEqual(got, want) {
			t.Errorf("splitWords(%q) = %q, want %q", in, got, want)
		}
	}
}
