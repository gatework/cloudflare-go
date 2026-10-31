// Package integration contains explicitly opt-in live API checks, never normal CI.
package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/gatework/cloudflare-go/internal/cloudflare"
	"github.com/gatework/cloudflare-go/internal/config"
	"github.com/gatework/cloudflare-go/internal/credentials"
	"github.com/gatework/cloudflare-go/internal/dns"
	"github.com/gatework/cloudflare-go/internal/output"
)

func TestLiveDNS(t *testing.T) {
	mode := os.Getenv("CLOUDFLARE_INTEGRATION")
	if mode == "" {
		t.Skip("set CLOUDFLARE_INTEGRATION=read or write and provide a dedicated test Zone")
	}
	if mode != "read" && mode != "write" {
		t.Fatal("integration mode must be read or write")
	}
	zoneID := os.Getenv("CLOUDFLARE_TEST_ZONE_ID")
	if !config.ValidID(zoneID) {
		t.Fatal("explicit CLOUDFLARE_TEST_ZONE_ID required")
	}
	token, err := credentials.Read("", "CLOUDFLARE_TEST_API_TOKEN")
	if err != nil {
		t.Fatal("test API token required")
	}
	client := cloudflare.New(token, "", nil)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	zone, err := client.GetZone(ctx, zoneID)
	if err != nil {
		t.Fatal(err)
	}
	records, err := client.ListRecords(ctx, zoneID, dns.Query{Domain: zone.Name}, 256<<20)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"json", "csv", "txt"} {
		var b bytes.Buffer
		if err = output.Render(&b, format, records, false); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "read" {
		return
	}
	random := make([]byte, 16)
	if _, err = rand.Read(random); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(random)
	name := "_cloudflare-go-test-" + nonce + "." + zone.Name
	if existing, err := client.ListRecords(ctx, zoneID, dns.Query{Domain: name, Exact: true}, 1<<20); err != nil || len(existing) != 0 {
		t.Fatal("test name is not available")
	}
	patch := dns.RecordPatch{"type": json.RawMessage(`"TXT"`), "name": raw(name), "content": raw("cloudflare-go integration " + nonce), "ttl": json.RawMessage(`1`)}
	created, err := client.CreateRecord(ctx, zoneID, patch)
	if err != nil {
		t.Fatalf("create outcome not confirmed; inspect test name %s before another write: %v", name, err)
	}
	// Cleanup only the exact ID returned by this run, and only while the unique
	// name/content still identify our own record. Never sweep by name prefix.
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
		defer stop()
		current, err := client.GetRecord(cleanupCtx, zoneID, created.ID)
		if err != nil || current.Name != name || current.Content != "cloudflare-go integration "+nonce {
			t.Errorf("test cleanup needs inspection for ID %s", created.ID)
			return
		}
		if err = client.DeleteRecord(cleanupCtx, zoneID, created.ID); err != nil {
			t.Error("test cleanup failed", err)
		}
	})
	if fetched, err := client.GetRecord(ctx, zoneID, created.ID); err != nil || fetched.Name != name {
		t.Fatal("created record could not be read", err)
	}
	if _, err = client.UpdateRecord(ctx, zoneID, created.ID, dns.RecordPatch{"comment": raw("cloudflare-go isolated test")}); err != nil {
		t.Fatal(err)
	}
}
func raw(value string) json.RawMessage { data, _ := json.Marshal(value); return data }
