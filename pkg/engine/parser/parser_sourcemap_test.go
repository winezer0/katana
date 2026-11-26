package parser

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/PuerkitoBio/goquery"
	"github.com/projectdiscovery/katana/pkg/navigation"
	"github.com/stretchr/testify/require"
)

func newSourceMapResponse(t *testing.T, rawURL, body, contentType string, request *navigation.Request) *navigation.Response {
	t.Helper()
	parsedURL, err := url.Parse(rawURL)
	require.NoError(t, err)
	reader, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	require.NoError(t, err)
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &navigation.Response{
		Resp: &http.Response{
			Request: &http.Request{URL: parsedURL},
			Header:  header,
		},
		Body:              body,
		Reader:            reader,
		Depth:             2,
		RootHostname:      parsedURL.Hostname(),
		NavigationRequest: request,
	}
}

func sourceMapParserWithOptions(options Options) *Parser {
	parser := NewResponseParser()
	parser.InitWithOptions(&options)
	return parser
}

func TestSourceMapFlagDiscoversReferenceAndEmbeddedEndpoints(t *testing.T) {
	parser := sourceMapParserWithOptions(Options{ScrapeJSMapResponses: true})

	jsResponse := newSourceMapResponse(t,
		"https://example.com/assets/app.js",
		"//# sourceMappingURL=app.js.map\n",
		"application/javascript",
		nil,
	)
	requests := parser.ParseResponse(jsResponse)
	require.Len(t, requests, 1)
	require.Equal(t, "https://example.com/assets/app.js.map", requests[0].URL)
	require.Equal(t, navigation.SourceMapRequestTag, requests[0].Tag)

	mapBody := `{"version":3,"sources":["webpack:///./src/api.ts"],"sourcesContent":["fetch('/api/users.json')"]}`
	mapResponse := newSourceMapResponse(t,
		"https://example.com/assets/app.js.map",
		mapBody,
		"application/json",
		&navigation.Request{URL: "https://example.com/assets/app.js.map", Tag: navigation.SourceMapRequestTag},
	)
	requests = parser.ParseResponse(mapResponse)
	var found bool
	for _, request := range requests {
		if request.URL == "https://example.com/api/users.json" {
			found = true
			require.Equal(t, "sourcemap-content", request.Tag)
			require.Equal(t, "https://example.com/src/api.ts", request.Source)
		}
	}
	require.True(t, found, "expected an endpoint extracted from sourcesContent; got %+v", requests)
}

func TestSourceMapFetchesMissingSourceContentAndParsesFetchedSource(t *testing.T) {
	parser := sourceMapParserWithOptions(Options{ScrapeJSMapResponses: true})
	mapBody := `{"version":3,"sources":["src/api.ts"],"sourcesContent":[null]}`
	mapResponse := newSourceMapResponse(t,
		"https://example.com/assets/app.js.map",
		mapBody,
		"application/json",
		&navigation.Request{URL: "https://example.com/assets/app.js.map", Tag: navigation.SourceMapRequestTag},
	)

	requests := parser.ParseResponse(mapResponse)
	require.Len(t, requests, 1)
	require.Equal(t, "https://example.com/assets/src/api.ts", requests[0].URL)
	require.Equal(t, navigation.SourceMapSourceTag, requests[0].Tag)

	sourceResponse := newSourceMapResponse(t,
		"https://example.com/assets/src/api.ts",
		"fetch('/api/from-source.json')",
		"text/plain",
		&navigation.Request{URL: "https://example.com/assets/src/api.ts", Tag: navigation.SourceMapSourceTag},
	)
	requests = parser.ParseResponse(sourceResponse)
	require.Len(t, requests, 1)
	require.Equal(t, "https://example.com/api/from-source.json", requests[0].URL)
}

func TestSourceMapFetchedSourceEndpointLimit(t *testing.T) {
	parser := sourceMapParserWithOptions(Options{ScrapeJSMapResponses: true})
	var content strings.Builder
	for i := 0; i < maxSourceMapEntries+1; i++ {
		_, err := fmt.Fprintf(&content, "fetch('/api/endpoint-%04d')\n", i)
		require.NoError(t, err)
	}

	sourceURL := "https://example.com/assets/src/api.js"
	response := newSourceMapResponse(t, sourceURL, content.String(), "application/javascript", &navigation.Request{
		URL: sourceURL,
		Tag: navigation.SourceMapSourceTag,
	})

	requests := parser.ParseResponse(response)
	require.Len(t, requests, maxSourceMapEntries)
}

func TestSourceMapFindsInlineAndIndexedExternalReferences(t *testing.T) {
	parser := sourceMapParserWithOptions(Options{ScrapeJSMapResponses: true})
	inlineHTML := `<html><script>//# sourceMappingURL=../maps/inline.js.map</script></html>`
	requests := parser.ParseResponse(newSourceMapResponse(t, "https://example.com/app/index.html", inlineHTML, "text/html", nil))
	require.Len(t, requests, 1)
	require.Equal(t, "https://example.com/maps/inline.js.map", requests[0].URL)

	indexedMap := `{"version":3,"sections":[{"offset":{"line":0,"column":0},"url":"parts/chunk.map"}]}`
	requests = parser.ParseResponse(newSourceMapResponse(t,
		"https://example.com/assets/app.js.map",
		indexedMap,
		"application/json",
		&navigation.Request{URL: "https://example.com/assets/app.js.map", Tag: navigation.SourceMapRequestTag},
	))
	require.Len(t, requests, 1)
	require.Equal(t, "https://example.com/assets/parts/chunk.map", requests[0].URL)
	require.Equal(t, navigation.SourceMapRequestTag, requests[0].Tag)
}

func TestSourceMapSkipsInvalidMapBodiesAndURLs(t *testing.T) {
	parser := sourceMapParserWithOptions(Options{ScrapeJSMapResponses: true})
	badReference := newSourceMapResponse(t, "https://example.com/app.js", "//# sourceMappingURL=data:application/json,{}", "application/javascript", nil)
	require.Empty(t, parser.ParseResponse(badReference))

	badMap := newSourceMapResponse(t,
		"https://example.com/app.js.map",
		`{"version":2,"sources":["src/api.js"]}`,
		"application/json",
		&navigation.Request{URL: "https://example.com/app.js.map", Tag: navigation.SourceMapRequestTag},
	)
	require.Empty(t, parser.ParseResponse(badMap))
}
