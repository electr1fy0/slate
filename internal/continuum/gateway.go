package continuum

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"

	"github.com/aws/aws-lambda-go/events"
)

// APIGatewayRouter adapts API Gateway HTTP API payload format 2.0 to Router.
type APIGatewayRouter struct {
	Router *Router
}

func (g *APIGatewayRouter) Handle(ctx context.Context, event events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	limit := int64(DefaultMaxRequestBytes)
	if g.Router != nil && g.Router.MaxRequestBytes > 0 {
		limit = g.Router.MaxRequestBytes
	}
	body, err := EncodeBody(event.Body, event.IsBase64Encoded, limit)
	if err != nil {
		return events.APIGatewayV2HTTPResponse{StatusCode: http.StatusRequestEntityTooLarge, Headers: map[string]string{"content-type": "text/plain; charset=utf-8"}, Body: "request body too large"}, nil
	}
	path := event.RawPath
	if path == "" {
		path = event.RequestContext.HTTP.Path
	}
	if path == "" {
		path = "/"
	}
	requestURL := &url.URL{Path: path, RawQuery: event.RawQueryString}
	if unescaped, unescapeErr := url.PathUnescape(path); unescapeErr == nil {
		requestURL.Path = unescaped
		requestURL.RawPath = path
	}
	method := event.RequestContext.HTTP.Method
	if method == "" {
		method = http.MethodGet
	}
	req := (&http.Request{Method: method, URL: requestURL, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}).WithContext(ctx)
	for key, value := range event.Headers {
		req.Header.Set(key, value)
	}
	for _, cookie := range event.Cookies {
		req.Header.Add("Cookie", cookie)
	}
	response := httptest.NewRecorder()
	if g.Router == nil {
		http.Error(response, "router is not configured", http.StatusServiceUnavailable)
	} else {
		g.Router.ServeHTTP(response, req)
	}
	result := response.Result()
	defer result.Body.Close()
	responseBody, _ := io.ReadAll(result.Body)
	headers := make(map[string]string, len(result.Header))
	for key, values := range result.Header {
		if strings.EqualFold(key, "Set-Cookie") {
			continue
		}
		if len(values) > 0 {
			headers[strings.ToLower(key)] = strings.Join(values, ",")
		}
	}
	out := events.APIGatewayV2HTTPResponse{StatusCode: result.StatusCode, Headers: headers, Body: string(responseBody)}
	for _, cookie := range result.Header.Values("Set-Cookie") {
		out.Cookies = append(out.Cookies, cookie)
	}
	if !isTextual(result.Header.Get("Content-Type")) {
		out.Body = base64.StdEncoding.EncodeToString(responseBody)
		out.IsBase64Encoded = true
	}
	return out, nil
}

func isTextual(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return true // API Gateway treats missing content type as text by default.
	}
	return strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" || mediaType == "application/problem+json" || mediaType == "application/javascript" || strings.HasSuffix(mediaType, "+json") || strings.HasSuffix(mediaType, "+xml") || mediaType == "application/xml" || mediaType == "application/x-www-form-urlencoded"
}
