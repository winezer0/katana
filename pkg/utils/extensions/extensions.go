package extensions

import (
	"path"
	"strings"

	"github.com/projectdiscovery/gologger"
	urlutil "github.com/projectdiscovery/utils/url"
)

// defaultDenylist is the default list of extensions to be denied
var defaultExtFilter = []string{".3g2", ".3gp", ".7z", ".apk", ".arj", ".avi", ".axd", ".bmp", ".csv", ".deb", ".dll", ".doc", ".drv", ".eot", ".exe", ".flv", ".gif", ".gifv", ".gz", ".h264", ".ico", ".iso", ".jar", ".jpeg", ".jpg", ".lock", ".m4a", ".m4v", ".map", ".mkv", ".mov", ".mp3", ".mp4", ".mpeg", ".mpg", ".msi", ".ogg", ".ogm", ".ogv", ".otf", ".pdf", ".pkg", ".png", ".ppt", ".psd", ".rar", ".rm", ".rpm", ".svg", ".swf", ".sys", ".tar.gz", ".tar", ".tif", ".tiff", ".ttf", ".txt", ".vob", ".wav", ".webm", ".webp", ".wmv", ".woff", ".woff2", ".xcf", ".xls", ".xlsx", ".zip"}

// Validator is a validator for file extension
type Validator struct {
	extensionsMatch          map[string]struct{}
	extensionsFilter         map[string]struct{}
	explicitExtensionsFilter map[string]struct{}
}

// NewValidator creates a new extension validator instance
func NewValidator(extensionsMatch, extensionsFilter []string, noDefaultExtFilter bool) *Validator {
	validator := &Validator{
		extensionsMatch:          make(map[string]struct{}),
		extensionsFilter:         make(map[string]struct{}),
		explicitExtensionsFilter: make(map[string]struct{}),
	}

	for _, extension := range extensionsMatch {
		validator.extensionsMatch[normalizeExtension(extension)] = struct{}{}
	}
	if !noDefaultExtFilter {
		for _, item := range defaultExtFilter {
			validator.extensionsFilter[normalizeExtension(item)] = struct{}{}
		}
	}
	for _, extension := range extensionsFilter {
		extension := normalizeExtension(extension)
		validator.extensionsFilter[extension] = struct{}{}
		validator.explicitExtensionsFilter[extension] = struct{}{}
	}
	return validator
}

// ValidatePath returns true if an extension is allowed by the validator
func (e *Validator) ValidatePath(item string) bool {
	return e.validatePath(item, false)
}

// ValidatePathAllowDefaultMap allows .map through the built-in denylist only;
// explicit extension filtering and extension matching still take precedence.
func (e *Validator) ValidatePathAllowDefaultMap(item string) bool {
	return e.validatePath(item, true)
}

func (e *Validator) validatePath(item string, allowDefaultMap bool) bool {
	var extension string
	u, err := urlutil.Parse(item)
	if err != nil {
		gologger.Warning().Msgf("validatepath: failed to parse url %v got %v", item, err)
		return false
	}
	if u.Path != "" {
		extension = strings.ToLower(path.Ext(u.Path))
	} else {
		extension = strings.ToLower(path.Ext(item))
	}
	if extension == "" && len(e.extensionsMatch) > 0 {
		_, ok := e.extensionsMatch[""]
		return ok
	}
	if len(e.extensionsMatch) > 0 {
		if _, ok := e.extensionsMatch[extension]; ok {
			return true
		}
		return false
	}

	if _, ok := e.explicitExtensionsFilter[extension]; ok {
		return false
	}
	if _, ok := e.extensionsFilter[extension]; ok && !(allowDefaultMap && extension == ".map") {
		return false
	}
	return true
}

func normalizeExtension(extension string) string {
	extension = strings.ToLower(extension)
	if extension == "none" {
		return ""
	}
	if strings.HasPrefix(extension, ".") {
		return extension
	}
	return "." + extension
}
