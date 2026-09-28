package chars

import "testing"

func TestStockLenCoversTheLongestPhrase(t *testing.T) {
	n := StockLen()
	if n < len("for what feels like an eternity") || n > MaxStockChars {
		t.Fatalf("hold back %d", n)
	}
	t.Logf("holding back %d characters", n)
}
