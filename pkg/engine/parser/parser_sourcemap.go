package parser

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/projectdiscovery/katana/pkg/navigation"
	"github.com/projectdiscovery/katana/pkg/utils"
)

const (
	maxSourceMapDepth    = 8
	maxSourceMapSections = 256
	maxSourceMapEntries  = 2000
)

var sourceMappingURLPattern = regexp.MustCompile(`(?m)(?://[#@]|/\*[#@])\s*sourceMappingURL\s*=\s*([^\s*]+)`)

type sourceMapDocument struct {
	Version        int                `json:"version"`
	SourceRoot     string             `json:"sourceRoot"`
	Sources        []string           `json:"sources"`
	SourcesContent []*string          `json:"sourcesContent"`
	Sections       []sourceMapSection `json:"sections"`
}

type sourceMapSection struct {
	Map json.RawMessage `json:"map"`
	URL string          `json:"url"`
}

type sourceMapEndpointExtractor func(string) []string

type sourceMapState struct {
	requests []*navigation.Request
	seen     map[string]struct{}
	sections int
}

// registerSourceMapParsers adds independent Source Map discovery and content
// handling. extraExtractor may provide jsluice results on supported builds.
func registerSourceMapParsers(p *Parser, extraExtractor sourceMapEndpointExtractor) {
	*p = append(*p,
		responseParser{bodyParser, scriptSourceMapInlineReferenceParser},
		responseParser{contentParser, scriptSourceMapFileReferenceParser},
		responseParser{contentParser, func(resp *navigation.Response) []*navigation.Request {
			return sourceMapContentParser(resp, extraExtractor)
		}},
	)
}

func scriptSourceMapInlineReferenceParser(resp *navigation.Response) []*navigation.Request {
	if resp == nil || resp.Reader == nil {
		return nil
	}
	var requests []*navigation.Request
	resp.Reader.Find("script").Each(func(_ int, item *goquery.Selection) {
		requests = append(requests, sourceMapReferenceRequests(item.Text(), resp)...)
	})
	return requests
}

func scriptSourceMapFileReferenceParser(resp *navigation.Response) []*navigation.Request {
	if !isJavaScriptSourceResponse(resp) {
		return nil
	}
	return sourceMapReferenceRequests(resp.Body, resp)
}

func sourceMapReferenceRequests(content string, resp *navigation.Response) []*navigation.Request {
	if resp == nil || resp.Resp == nil || resp.Resp.Request == nil || resp.Resp.Request.URL == nil {
		return nil
	}
	baseURL := resp.Resp.Request.URL
	seen := make(map[string]struct{})
	var requests []*navigation.Request
	for _, match := range sourceMappingURLPattern.FindAllStringSubmatch(content, -1) {
		if len(match) < 2 {
			continue
		}
		raw := strings.TrimRight(strings.TrimSpace(match[1]), ";")
		target := resolveHTTPURL(raw, baseURL)
		if target == nil {
			continue
		}
		key := target.String()
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		requests = append(requests, newSourceMapRequest(target, resp, navigation.SourceMapRequestTag, "sourceMappingURL"))
	}
	return requests
}

func sourceMapContentParser(resp *navigation.Response, extraExtractor sourceMapEndpointExtractor) []*navigation.Request {
	if resp == nil || resp.Resp == nil || resp.Resp.Request == nil || resp.Resp.Request.URL == nil {
		return nil
	}
	currentURL := resp.Resp.Request.URL
	if resp.NavigationRequest != nil && resp.NavigationRequest.Tag == navigation.SourceMapSourceTag {
		return extractSourceMapEndpoints(resp.Body, currentURL, resp, extraExtractor)
	}
	if !looksLikeSourceMap(currentURL.Path, resp.Body) {
		return nil
	}
	state := &sourceMapState{seen: make(map[string]struct{})}
	state.parse([]byte(resp.Body), currentURL, resp, extraExtractor, 0)
	return state.requests
}

func extractSourceMapEndpoints(content string, baseURL *url.URL, resp *navigation.Response, extraExtractor sourceMapEndpointExtractor) []*navigation.Request {
	if baseURL == nil || content == "" {
		return nil
	}
	seen := make(map[string]struct{})
	var requests []*navigation.Request
	extractors := []sourceMapEndpointExtractor{utils.ExtractRelativeEndpoints}
	if extraExtractor != nil {
		extractors = append(extractors, extraExtractor)
	}
	for _, extract := range extractors {
		for _, endpoint := range extract(content) {
			if len(requests) >= maxSourceMapEntries {
				return requests
			}
			target := resolveHTTPURL(endpoint, baseURL)
			if target == nil {
				continue
			}
			key := target.String()
			if _, duplicate := seen[key]; duplicate {
				continue
			}
			seen[key] = struct{}{}
			request := newSourceMapRequest(target, resp, "sourcemap-content", "endpoint")
			request.Source = baseURL.String()
			requests = append(requests, request)
		}
	}
	return requests
}

func (x *sourceMapState) parse(body []byte, mapURL *url.URL, resp *navigation.Response, extraExtractor sourceMapEndpointExtractor, depth int) {
	if x == nil || mapURL == nil || len(x.requests) >= maxSourceMapEntries || depth > maxSourceMapDepth {
		return
	}
	var doc sourceMapDocument
	if err := json.Unmarshal(body, &doc); err != nil || doc.Version != 3 {
		return
	}
	if len(doc.Sections) > 0 {
		for _, section := range doc.Sections {
			if x.sections >= maxSourceMapSections || len(x.requests) >= maxSourceMapEntries {
				return
			}
			x.sections++
			switch {
			case len(section.Map) > 0:
				x.parse(section.Map, mapURL, resp, extraExtractor, depth+1)
			case section.URL != "":
				target := resolveHTTPURL(section.URL, mapURL)
				if target != nil {
					x.add(newSourceMapRequest(target, resp, navigation.SourceMapRequestTag, "section"))
				}
			}
		}
		return
	}

	for i, source := range doc.Sources {
		if len(x.requests) >= maxSourceMapEntries || isIgnoredSourceMapSource(source) {
			continue
		}
		sourceURL := resolveSourceMapSource(source, doc.SourceRoot, mapURL)
		if i < len(doc.SourcesContent) && doc.SourcesContent[i] != nil && strings.TrimSpace(*doc.SourcesContent[i]) != "" {
			baseURL := sourceURL
			if baseURL == nil {
				baseURL = mapURL
			}
			for _, req := range extractSourceMapEndpoints(*doc.SourcesContent[i], baseURL, resp, extraExtractor) {
				x.add(req)
			}
			continue
		}
		if sourceURL != nil {
			x.add(newSourceMapRequest(sourceURL, resp, navigation.SourceMapSourceTag, source))
		}
	}
	// Some producers omit sources while retaining sourcesContent. Preserve those
	// contents by scanning them against the map URL as a best-effort base.
	for i := len(doc.Sources); i < len(doc.SourcesContent) && len(x.requests) < maxSourceMapEntries; i++ {
		if content := doc.SourcesContent[i]; content != nil {
			for _, req := range extractSourceMapEndpoints(*content, mapURL, resp, extraExtractor) {
				x.add(req)
			}
		}
	}
}

func (x *sourceMapState) add(req *navigation.Request) {
	if req == nil || req.URL == "" || len(x.requests) >= maxSourceMapEntries {
		return
	}
	key := req.Method + "\x00" + req.URL
	if _, duplicate := x.seen[key]; duplicate {
		return
	}
	x.seen[key] = struct{}{}
	x.requests = append(x.requests, req)
}

func newSourceMapRequest(target *url.URL, resp *navigation.Response, tag, attribute string) *navigation.Request {
	if target == nil || resp == nil {
		return nil
	}
	source := ""
	if resp.Resp != nil && resp.Resp.Request != nil && resp.Resp.Request.URL != nil {
		source = resp.Resp.Request.URL.String()
	}
	return &navigation.Request{
		Method:       http.MethodGet,
		URL:          target.String(),
		Depth:        resp.Depth,
		RootHostname: resp.RootHostname,
		Source:       source,
		Tag:          tag,
		Attribute:    attribute,
	}
}

func looksLikeSourceMap(resourcePath, body string) bool {
	if strings.HasSuffix(strings.ToLower(resourcePath), ".map") {
		return true
	}
	trimmed := strings.TrimSpace(body)
	if len(trimmed) > 4096 {
		trimmed = trimmed[:4096]
	}
	return strings.Contains(trimmed, `"version"`) &&
		(strings.Contains(trimmed, `"sources"`) || strings.Contains(trimmed, `"sections"`))
}

func isJavaScriptSourceResponse(resp *navigation.Response) bool {
	if resp == nil || resp.Resp == nil || resp.Resp.Request == nil || resp.Resp.Request.URL == nil {
		return false
	}
	extension := strings.ToLower(path.Ext(resp.Resp.Request.URL.Path))
	switch extension {
	case ".js", ".mjs", ".cjs", ".jsx", ".ts", ".tsx":
		return true
	}
	contentType := strings.ToLower(resp.Resp.Header.Get("Content-Type"))
	return strings.Contains(contentType, "/javascript") || strings.Contains(contentType, "/ecmascript") || strings.Contains(contentType, "/typescript")
}

func resolveSourceMapSource(source, sourceRoot string, mapURL *url.URL) *url.URL {
	source = strings.TrimSpace(strings.ReplaceAll(source, `\`, "/"))
	if source == "" || mapURL == nil || isIgnoredSourceMapSource(source) {
		return nil
	}
	sourceURL, err := url.Parse(source)
	if err != nil {
		return nil
	}
	if sourceURL.IsAbs() {
		if isHTTPURL(sourceURL) {
			return normalizeHTTPPath(sourceURL)
		}
		if sourceURL.Scheme == "webpack" || sourceURL.Scheme == "webpack-internal" {
			return webpackSourceURL(sourceURL, mapURL)
		}
		return nil
	}

	root := strings.TrimSpace(strings.ReplaceAll(sourceRoot, `\`, "/"))
	if root != "" {
		rootURL, rootErr := url.Parse(root)
		if rootErr == nil && isHTTPURL(rootURL) {
			return normalizeHTTPPath(rootURL.ResolveReference(sourceURL))
		}
		if rootErr == nil && (rootURL.Scheme == "webpack" || rootURL.Scheme == "webpack-internal") {
			combined := path.Join(rootURL.Path, sourceURL.Path)
			return resolveWebpackPath(combined, mapURL)
		}
		if strings.HasPrefix(root, "/") {
			rootURL := *mapURL
			rootURL.Path = path.Join(root, sourceURL.Path)
			return normalizeHTTPPath(&rootURL)
		}
		return normalizeHTTPPath(mapURL.ResolveReference(&url.URL{Path: path.Join(root, sourceURL.Path)}))
	}
	return normalizeHTTPPath(mapURL.ResolveReference(sourceURL))
}

func webpackSourceURL(sourceURL, mapURL *url.URL) *url.URL {
	return resolveWebpackPath(sourceURL.Path, mapURL)
}

func resolveWebpackPath(sourcePath string, mapURL *url.URL) *url.URL {
	clean := path.Clean("/" + strings.TrimLeft(sourcePath, "/"))
	if clean == "/" || clean == "/." {
		return nil
	}
	result := *mapURL
	result.Path = clean
	result.RawPath = ""
	result.RawQuery = ""
	result.Fragment = ""
	return normalizeHTTPPath(&result)
}

func normalizeHTTPPath(target *url.URL) *url.URL {
	if !isHTTPURL(target) {
		return nil
	}
	target = cloneURL(target)
	target.Path = path.Clean("/" + strings.TrimLeft(target.Path, "/"))
	target.RawPath = ""
	target.Fragment = ""
	return target
}

func resolveHTTPURL(raw string, base *url.URL) *url.URL {
	if base == nil {
		return nil
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "" && !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return nil
	}
	return normalizeHTTPPath(base.ResolveReference(parsed))
}

func isHTTPURL(target *url.URL) bool {
	return target != nil && (strings.EqualFold(target.Scheme, "http") || strings.EqualFold(target.Scheme, "https")) && target.Host != ""
}

func cloneURL(target *url.URL) *url.URL {
	copy := *target
	return &copy
}

func isIgnoredSourceMapSource(source string) bool {
	lower := strings.ToLower(strings.ReplaceAll(source, `\`, "/"))
	return strings.Contains(lower, "node_modules/") || strings.Contains(lower, "/webpack/runtime/") ||
		strings.HasPrefix(lower, "webpack://webpack/") || strings.HasPrefix(lower, "webpack-internal://webpack/")
}
