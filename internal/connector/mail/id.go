package mail

import (
	id "github.com/ProtonMail/go-imap-id"
	"github.com/emersion/go-imap/client"
)

// idCommand sends RFC 2971 ID. NetEase (163/126/yeah) rejects SELECT with
// "Unsafe Login" unless the client identifies itself first.
func idCommand(cl *client.Client) error {
	ok, err := cl.Support(id.Capability)
	if err != nil || !ok {
		return err
	}
	_, err = id.NewClient(cl).ID(id.ID{id.FieldName: "life-agent", id.FieldVersion: "0.1"})
	return err
}
