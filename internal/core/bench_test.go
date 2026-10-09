package core

import (
	"strings"
	"testing"
)

func benchInput(b *testing.B) string {
	var sb strings.Builder
	for _, src := range readFixtures(b) {
		sb.WriteString(src)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func BenchmarkLex(b *testing.B) {
	src := benchInput(b)
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, ch := Lex("bench", src)
		for range ch {
		}
	}
}

func BenchmarkParse(b *testing.B) {
	src := benchInput(b)
	b.SetBytes(int64(len(src)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ParseString("bench", src)
	}
}
