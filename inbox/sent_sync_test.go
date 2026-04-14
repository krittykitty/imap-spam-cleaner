package inbox

import (
	"sort"
	"testing"

	"github.com/dominicgisler/imap-spam-cleaner/imap"
)

func TestExtractRecipientEmails_IncludesToCcBcc(t *testing.T) {
	m := imap.Message{
		To:  "Alice <alice@example.com>, bob@example.com",
		Cc:  "Carol <carol@example.com>",
		Bcc: "dave@example.com",
	}
	got := extractRecipientEmails(m)
	want := []string{"alice@example.com", "bob@example.com", "carol@example.com", "dave@example.com"}

	sort.Strings(got)
	sort.Strings(want)

	if len(got) != len(want) {
		t.Fatalf("unexpected number of recipients: got=%d want=%d; got=%v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("mismatch at index %d: got=%s want=%s; got=%v", i, got[i], want[i], got)
		}
	}
}
