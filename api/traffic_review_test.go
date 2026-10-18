package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alireza0/s-ui/service"
)

func reviewRequest(t *testing.T, client *http.Client, method, address, origin string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, address, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if len(b) > 0 && json.Unmarshal(b, &body) != nil {
		body = nil
	}
	return resp.StatusCode, body
}

func TestTrafficReviewRequiresRealCookieAndIsReadOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "review.db")
	t.Setenv("SUI_TRAFFIC_REVIEW_DB", path)
	if err := service.StartTrafficReview(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.StopTrafficReview)
	srv := httptest.NewServer(loginEngine(t))
	defer srv.Close()
	base := srv.URL + "/api/traffic-review/"
	query := "?date=2026-10-08&timezone=America%2FLos_Angeles"

	// XHR plus an API bearer token is not a panel session.
	anonymous := &http.Client{Transport: srv.Client().Transport}
	// A normal API client also gets JSON/401, without a redirect to /login
	// outside the panel's configured base path.
	plain, plainErr := anonymous.Get(base + "summary" + query)
	if plainErr != nil {
		t.Fatal(plainErr)
	}
	plain.Body.Close()
	if plain.StatusCode != http.StatusUnauthorized {
		t.Fatalf("non-XHR review request returned %d", plain.StatusCode)
	}
	for _, action := range []string{"summary", "series", "destinations", "alerts", "health", "billing", "billing-overview", "candidates"} {
		status, body := reviewRequest(t, anonymous, http.MethodGet, base+action+query, "")
		if status != http.StatusUnauthorized || body["success"] != false {
			t.Fatalf("anonymous %s: %d %+v", action, status, body)
		}
	}
	request, _ := http.NewRequest(http.MethodGet, base+"summary"+query, nil)
	request.Header.Set("Authorization", "Bearer fake-api-token")
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	response, err := anonymous.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("token alone returned %d", response.StatusCode)
	}

	jar, _ := cookiejar.New(nil)
	client := srv.Client()
	client.Jar = jar
	form := url.Values{"user": {"admin"}, "pass": {"admin"}}
	login, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/login", strings.NewReader(form.Encode()))
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	login.Header.Set("Origin", srv.URL)
	response, err = client.Do(login)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login returned %d", response.StatusCode)
	}
	findSessionCookie(t, response)
	// A real apiv2 token grants no access to this cookie-only endpoint.
	tokenForm := url.Values{"expiry": {"1"}, "desc": {"review-auth-test"}}
	createToken, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/addToken", strings.NewReader(tokenForm.Encode()))
	createToken.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	createToken.Header.Set("Origin", srv.URL)
	response, err = client.Do(createToken)
	if err != nil {
		t.Fatal(err)
	}
	var tokenBody struct {
		Success bool   `json:"success"`
		Obj     string `json:"obj"`
	}
	if err := json.NewDecoder(response.Body).Decode(&tokenBody); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !tokenBody.Success || tokenBody.Obj == "" {
		t.Fatalf("could not create API token: %d %+v", response.StatusCode, tokenBody)
	}
	tokenOnly, _ := http.NewRequest(http.MethodGet, base+"summary"+query, nil)
	tokenOnly.Header.Set("Token", tokenBody.Obj)
	tokenOnly.Header.Set("X-Requested-With", "XMLHttpRequest")
	response, err = anonymous.Do(tokenOnly)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("real API token alone returned %d", response.StatusCode)
	}

	for _, action := range []string{"summary", "series", "destinations", "alerts", "health", "billing", "billing-overview", "candidates"} {
		status, body := reviewRequest(t, client, http.MethodGet, base+action+query, "")
		if status != http.StatusOK || body["success"] != true {
			t.Fatalf("authenticated %s: %d %+v", action, status, body)
		}
		obj := body["obj"]
		if obj == nil {
			t.Fatalf("%s returned null obj", action)
		}
		switch action {
		case "summary":
			v, ok := obj.(map[string]any)
			if !ok || v["ruleVersion"] == nil || v["disclaimer"] == nil || v["flowBillingConfirmed"] != false || v["billingOverview"] == nil {
				t.Fatalf("summary contract: %+v", obj)
			}
		case "billing-overview":
			v, ok := obj.(map[string]any)
			if !ok || v["targetSKUs"] == nil || v["standardTierReference"] == nil || v["syncStatus"] == nil {
				t.Fatalf("billing-overview contract: %+v", obj)
			}
		case "candidates":
			v, ok := obj.(map[string]any)
			if !ok || v["flowBillingConfirmed"] != false || v["relaySuggestion"] == nil {
				t.Fatalf("candidates contract: %+v", obj)
			}
		case "health":
			v, ok := obj.(map[string]any)
			if !ok || v["status"] == nil || v["conntrack"] == nil {
				t.Fatalf("health contract: %+v", obj)
			}
		default:
			if _, ok := obj.([]any); !ok {
				t.Fatalf("%s should return an array, got %T", action, obj)
			}
		}
	}

	// Every read surface accepts the same inclusive calendar-date window. A
	// three-year request must not be rejected by the former 31-day limit.
	for _, action := range []string{"summary", "series", "destinations", "alerts", "health", "billing", "billing-overview", "candidates"} {
		status, body := reviewRequest(t, client, http.MethodGet, base+action+"?startDate=2024-01-01&endDate=2026-12-31&timezone=UTC", "")
		if status != http.StatusOK || body["success"] != true {
			t.Fatalf("three-year %s: %d %+v", action, status, body)
		}
		if action == "summary" {
			obj := body["obj"].(map[string]any)
			if obj["from"] != float64(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).Unix()) || obj["to"] != float64(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).Unix()) {
				t.Fatalf("inclusive calendar range changed: %+v", obj)
			}
			if obj["networkSampleCount"] != float64(0) || obj["proxySampleCount"] != float64(0) || obj["egressDifferenceBytes"] != float64(0) {
				t.Fatalf("empty measurements need explicit observation counts: %+v", obj)
			}
		}
	}
	for _, bad := range []string{
		"?timezone=Europe%2FLondon", "?date=2026-10-08&from=1", "?from=-1&to=2", "?from=10&to=9", "?limit=0", "?limit=501", "?date=not-a-date",
		"?startDate=2024-01-01", "?startDate=2024-01-01&endDate=2027-01-01", "?startDate=2026-10-18&endDate=2026-10-17",
		"?startDate=2024-01-01&endDate=2024-01-01&date=2024-01-01", "?startDate=2024-01-01&startDate=2025-01-01&endDate=2026-01-01",
	} {
		status, body := reviewRequest(t, client, http.MethodGet, base+"summary"+bad, "")
		if status != http.StatusBadRequest || body["success"] != false {
			t.Fatalf("bad range %s: %d %+v", bad, status, body)
		}
	}
	if status, _ := reviewRequest(t, client, http.MethodGet, base+"unknown"+query, ""); status != http.StatusNotFound {
		t.Fatalf("unknown action status %d", status)
	}
	if status, _ := reviewRequest(t, client, http.MethodPost, base+"summary"+query, srv.URL); status != http.StatusNotFound {
		t.Fatalf("POST must not mutate review: status %d", status)
	}
	if status, body := reviewRequest(t, client, http.MethodGet, base+"billing"+query, ""); status != http.StatusOK || len(body["obj"].([]any)) != 0 {
		t.Fatalf("POST changed billing: %d %+v", status, body)
	}

	status, _ := reviewRequest(t, client, http.MethodGet, srv.URL+"/api/logout", "")
	if status != http.StatusOK {
		t.Fatalf("logout status %d", status)
	}
	status, body := reviewRequest(t, client, http.MethodGet, base+"summary"+query, "")
	if status != http.StatusUnauthorized || body["success"] != false {
		t.Fatalf("session survives logout: %d %+v", status, body)
	}
}
