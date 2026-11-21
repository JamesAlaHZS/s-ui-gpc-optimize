package trafficreview

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoverBillingTable(t *testing.T) {
	for _, tc := range []struct {
		name, secondPage, want string
		pending, ambiguous     bool
	}{
		{name: "uses selected dataset and finds second-page table", secondPage: "gcp_billing_export_v1_ABCDEF_123456", want: "gcp_billing_export_v1_ABCDEF_123456"},
		{name: "waits for Google to create the export", pending: true},
		{name: "asks for the account when multiple export tables exist", secondPage: "gcp_billing_export_v1_SECOND", ambiguous: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					if r.Header.Get("Metadata-Flavor") != "Google" {
						t.Error("metadata header is missing")
					}
					fmt.Fprint(w, `{"access_token":"test-token"}`)
				case "/projects/example-dataset-project/datasets/owned_dataset/tables":
					if r.Header.Get("Authorization") != "Bearer test-token" {
						t.Error("missing authorization")
					}
					if r.URL.Query().Get("pageToken") == "" {
						if tc.ambiguous {
							fmt.Fprint(w, `{"nextPageToken":"more","tables":[{"type":"TABLE","tableReference":{"tableId":"gcp_billing_export_v1_FIRST1"}}]}`)
						} else {
							fmt.Fprint(w, `{"nextPageToken":"more","tables":[{"type":"TABLE","tableReference":{"tableId":"gcp_billing_export_resource_v1_OTHER1"}},{"type":"VIEW","tableReference":{"tableId":"gcp_billing_export_v1_IGNORE"}}]}`)
						}
					} else if tc.pending {
						fmt.Fprint(w, `{}`)
					} else {
						fmt.Fprintf(w, `{"tables":[{"type":"TABLE","tableReference":{"tableId":%q}}]}`, tc.secondPage)
					}
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			c := &ADCBigQueryClient{HTTPClient: server.Client(), MetadataTokenURL: server.URL + "/token", BigQueryBaseURL: server.URL}
			got, err := c.DiscoverBillingTable(context.Background(), "example-dataset-project", "owned_dataset")
			switch {
			case tc.pending:
				if !errors.Is(err, ErrBillingExportPending) {
					t.Fatalf("expected pending export, got %q %v", got, err)
				}
			case tc.ambiguous:
				if err == nil || errors.Is(err, ErrBillingExportPending) || got != "" {
					t.Fatalf("expected ambiguity error, got %q %v", got, err)
				}
			default:
				if got != tc.want || err != nil {
					t.Fatalf("table = %q (%v), want %q", got, err, tc.want)
				}
			}
		})
	}
}
