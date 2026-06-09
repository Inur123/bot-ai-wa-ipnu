package knowledge

import (
	"strings"
	"testing"
)

func TestLocalProbe(t *testing.T) {
	result := SearchWithLimit("siapa bendahara ipnu magetan", 4000)
	if strings.TrimSpace(result) == "" {
		t.Fatal("knowledge lokal kosong atau tidak terbaca")
	}
	t.Log(result)
}
