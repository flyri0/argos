package budget

import "testing"

func ptr(n int64) *int64 { return &n }

func TestAccountBalance_EmptyAccount(t *testing.T) {
	got := AccountBalance("acc-1", nil)
	if got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestAccountBalance_MixedPositiveAndNegative(t *testing.T) {
	transactions := []Transaction{
		{AccountID: "acc-1", Amount: 10000},
		{AccountID: "acc-1", Amount: -2500},
		{AccountID: "acc-1", Amount: -1000},
		{AccountID: "acc-2", Amount: 999999}, // different account, must not count
	}

	got := AccountBalance("acc-1", transactions)
	want := int64(10000 - 2500 - 1000)
	if got != want {
		t.Fatalf("expected %d, got %d", want, got)
	}
}

func TestAccountBalance_ExcludesSoftDeleted(t *testing.T) {
	transactions := []Transaction{
		{AccountID: "acc-1", Amount: 5000},
		{AccountID: "acc-1", Amount: -5000, DeletedAt: ptr(1732400000000)},
		{AccountID: "acc-1", Amount: 3000, DeletedAt: ptr(1732400000000)},
	}

	got := AccountBalance("acc-1", transactions)
	want := int64(5000)
	if got != want {
		t.Fatalf("expected %d (deleted rows excluded), got %d", want, got)
	}
}
