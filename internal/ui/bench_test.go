package ui

import (
	"strings"
	"testing"
)

// A realistic reply: narration in asterisks, speech in quotes, a few hundred
// words. Markup runs over this every time a message is rendered, and over the
// whole transcript when a chat is opened.
var benchReply = strings.Repeat(
	`*She freezes mid-laugh as your arm grazes her side, the playful wrestling halting under the weight of sudden realization.* `+
		`"Oh... I-I'm sorry," *she whispers, her face flushing a deep crimson as she looks down at her hands.* `+
		`*She doesn't pull away, instead waiting quietly for your next move while glancing nervously toward the door.* `, 4)

func BenchmarkMarkupRoleplay(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchReply)))
	for i := 0; i < b.N; i++ {
		_ = Markup(benchReply, Roleplay)
	}
}

func BenchmarkMarkupPlain(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(benchReply)))
	for i := 0; i < b.N; i++ {
		_ = Markup(benchReply, Plain)
	}
}

// Snippet runs once per sidebar row, on every sidebar rebuild.
func BenchmarkSnippet(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = Snippet(benchReply, 60)
	}
}
