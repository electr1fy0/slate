package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/exams"
	"github.com/ayush/slate/internal/model"
)

func TestQualityRequestParsing(t *testing.T) {
	for _, body := range []string{"null", "[]", "42", "{} {}"} {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		response := httptest.NewRecorder()
		var value struct {
			Name string `json:"name"`
		}
		if decode(response, request, &value) || response.Code != 422 {
			t.Fatalf("accepted body %q", body)
		}
	}
	for _, value := range []string{"12oops", "12%202", "%2012"} {
		request := httptest.NewRequest(http.MethodGet, "/?limit="+value, nil)
		if queryInt(request, "limit", 50) != 50 {
			t.Fatalf("accepted %q", value)
		}
	}
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := randomSuffix()
		if seen[id] {
			t.Fatal("generated duplicate quiz IDs")
		}
		seen[id] = true
	}
	rows := submissionMaps([]model.Submission{{UpdatedAt: 100}, {UpdatedAt: 9}, {UpdatedAt: 20}})
	if rows[0]["updated_at"].(int64) != 9 || rows[2]["updated_at"].(int64) != 100 {
		t.Fatal("timestamp sort is not numeric")
	}
}

func TestQualityCORSCacheAndAuthorization(t *testing.T) {
	settings := config.Defaults()
	settings.CORSOrigins = []string{"https://school.example"}
	server := &Server{Settings: settings}
	handler := server.cors(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if !strings.Contains(response.Header().Get("Vary"), "Origin") {
		t.Fatal("missing Vary on an originless request")
	}
	request := httptest.NewRequest(http.MethodOptions, "/", nil)
	request.Header.Set("Origin", "https://school.example")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if !strings.Contains(response.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatal("bearer header not allowed")
	}
	if !strings.Contains(response.Header().Get("Access-Control-Expose-Headers"), "Retry-After") {
		t.Fatal("retry delay not exposed")
	}
}

func TestQualityAttemptValidationIsNotAnOutage(t *testing.T) {
	response := httptest.NewRecorder()
	server := &Server{}
	if !server.writeExamError(response, exams.ErrInvalidRequest) || response.Code != 422 {
		t.Fatalf("attempt validation status = %d", response.Code)
	}
}
