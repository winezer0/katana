package types

import (
	"testing"

	"github.com/projectdiscovery/katana/pkg/navigation"
	"github.com/projectdiscovery/katana/pkg/utils/extensions"
	"github.com/stretchr/testify/require"
)

func TestValidateRequestPathForSourceMaps(t *testing.T) {
	newOptions := func(enabled bool, filtered []string) *CrawlerOptions {
		return &CrawlerOptions{
			Options:             &Options{ScrapeJSMapResponses: enabled},
			ExtensionsValidator: extensions.NewValidator(nil, filtered, false),
		}
	}

	t.Run("tagged source map request bypasses only default denylist", func(t *testing.T) {
		crawlerOptions := newOptions(true, nil)
		require.True(t, crawlerOptions.ValidateRequestPath(&navigation.Request{
			URL: "https://example.com/app.js.map", Tag: navigation.SourceMapRequestTag,
		}))
		require.False(t, crawlerOptions.ValidateRequestPath(&navigation.Request{
			URL: "https://example.com/app.js.map",
		}))
	})

	t.Run("feature flag and explicit filters remain effective", func(t *testing.T) {
		require.False(t, newOptions(false, nil).ValidateRequestPath(&navigation.Request{
			URL: "https://example.com/app.js.map", Tag: navigation.SourceMapRequestTag,
		}))
		require.False(t, newOptions(true, []string{"map"}).ValidateRequestPath(&navigation.Request{
			URL: "https://example.com/app.js.map", Tag: navigation.SourceMapRequestTag,
		}))
	})

	t.Run("explicit map seed is allowed when enabled", func(t *testing.T) {
		require.True(t, newOptions(true, nil).ValidateRequestPath(&navigation.Request{
			URL: "https://example.com/app.js.map", SkipValidation: true,
		}))
	})
}
