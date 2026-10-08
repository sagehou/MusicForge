package forge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Runtime struct {
	ConfigDir      string         `json:"-"`
	Listen         string         `json:"listen"`
	PublicURL      string         `json:"public_url"`
	AllowedOrigins []string       `json:"allowed_origins,omitempty"`
	LogLevel       string         `json:"log_level"`
	FFmpeg         string         `json:"ffmpeg"`
	FFprobe        string         `json:"ffprobe"`
	TrustedProxies []netip.Prefix `json:"trusted_proxies,omitempty"`
	SourceTimeoutSeconds int `json:"source_timeout_seconds,omitempty"`
}

func LoadRuntime(dir string) (Runtime, error) {
	c := Runtime{ConfigDir: dir, Listen: ":8787", LogLevel: "INFO", FFmpeg: "ffmpeg", FFprobe: "ffprobe"}
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err == nil {
		if err := json.Unmarshal(b, &c); err != nil {
			return c, err
		}
	} else if !os.IsNotExist(err) {
		return c, err
	}
	for key, target := range map[string]*string{"LISTEN": &c.Listen, "PUBLIC_URL": &c.PublicURL, "LOG_LEVEL": &c.LogLevel, "FFMPEG": &c.FFmpeg, "FFPROBE": &c.FFprobe} {
		if value := os.Getenv("MUSICFORGE_" + key); value != "" {
			*target = value
		}
	}
	if value, ok := os.LookupEnv("MUSICFORGE_SOURCE_TIMEOUT_SECONDS"); ok {
		c.SourceTimeoutSeconds, err = strconv.Atoi(value)
		if err != nil || c.SourceTimeoutSeconds < 1 || c.SourceTimeoutSeconds > 3600 {
			return c, errors.New("source_timeout_seconds must be 1–3600")
		}
	}
	if c.SourceTimeoutSeconds < 0 || c.SourceTimeoutSeconds > 3600 {
		return c, errors.New("source_timeout_seconds must be 1–3600 (or omitted for 120)")
	}
	if value, ok := os.LookupEnv("MUSICFORGE_ALLOWED_ORIGINS"); ok {
		c.AllowedOrigins = nil
		if strings.TrimSpace(value) != "" {
			c.AllowedOrigins = strings.Split(value, ",")
		}
	}
	if value, ok := os.LookupEnv("MUSICFORGE_TRUSTED_PROXIES"); ok {
		c.TrustedProxies = nil
		if strings.TrimSpace(value) != "" {
			for _, raw := range strings.Split(value, ",") {
				prefix, err := netip.ParsePrefix(strings.TrimSpace(raw))
				if err != nil {
					return c, errors.New("trusted_proxies must contain IP CIDRs separated by commas")
				}
				c.TrustedProxies = append(c.TrustedProxies, prefix)
			}
		}
	}
	for _, prefix := range c.TrustedProxies {
		if !prefix.IsValid() {
			return c, errors.New("trusted_proxies must contain valid IP CIDRs")
		}
	}
	if c.PublicURL != "" {
		c.PublicURL, err = normalizeOrigin(c.PublicURL)
		if err != nil {
			return c, errors.New("public_url must be an HTTP(S) origin without a path")
		}
	}
	origins := c.AllowedOrigins
	c.AllowedOrigins = nil
	for _, raw := range origins {
		origin, err := normalizeOrigin(raw)
		if err != nil {
			return c, errors.New("allowed_origins must contain HTTP(S) origins without paths or wildcards")
		}
		duplicate := false
		newURL, _ := url.Parse(origin)
		for _, existing := range c.origins() {
			if origin == existing {
				duplicate = true
			}
			oldURL, _ := url.Parse(existing)
			// Cookies are scoped to hostnames, not ports; mixed schemes would collide.
			if oldURL.Hostname() == newURL.Hostname() && oldURL.Scheme != newURL.Scheme {
				return c, errors.New("configure only one scheme per origin hostname")
			}
		}
		if !duplicate {
			c.AllowedOrigins = append(c.AllowedOrigins, origin)
		}
	}
	return c, nil
}

func normalizeOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.Path != "" || strings.ContainsAny(raw, "?#") {
		return "", errors.New("invalid HTTP(S) origin")
	}
	host := strings.ToLower(u.Hostname())
	if strings.HasPrefix(u.Host, "[") {
		ip, err := netip.ParseAddr(u.Hostname())
		if err != nil || !ip.Is6() || ip.Zone() != "" {
			return "", errors.New("invalid origin IP address")
		}
		host = "[" + ip.String() + "]"
	} else if strings.IndexFunc(u.Hostname(), func(c rune) bool {
		return !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_')
	}) >= 0 {
		return "", errors.New("invalid origin hostname")
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", errors.New("invalid origin port")
		}
		port = strconv.Itoa(n)
	} else if strings.HasSuffix(u.Host, ":") {
		return "", errors.New("empty origin port")
	}
	if u.Scheme == "http" && port == "80" || u.Scheme == "https" && port == "443" {
		port = ""
	}
	if port != "" {
		host += ":" + port
	}
	return u.Scheme + "://" + host, nil
}

func (c Runtime) origins() []string {
	origins := []string{}
	if c.PublicURL != "" {
		origins = append(origins, c.PublicURL)
	}
	return append(origins, c.AllowedOrigins...)
}

type Encoding struct {
	Codec   string `json:"codec"`
	Mode    string `json:"mode"`
	Bitrate int    `json:"bitrate"`
	Quality int    `json:"quality"`
}

func (e Encoding) Fingerprint() string {
	if e.Codec != "mp3" || e.Mode != "vbr" {
		e.Quality = 0
	} else {
		e.Bitrate = 0
	}
	b, _ := json.Marshal(e)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (e Encoding) Validate() error {
	if e.Codec != "opus" && e.Codec != "mp3" {
		return errors.New("codec must be opus or mp3")
	}
	if e.Mode != "vbr" && e.Mode != "cbr" {
		return errors.New("mode must be vbr or cbr")
	}
	if e.Codec == "mp3" && e.Mode == "vbr" {
		if e.Quality < 0 || e.Quality > 9 {
			return errors.New("MP3 VBR quality must be 0–9")
		}
		return nil
	}
	if e.Codec == "opus" {
		if e.Bitrate < 32 || e.Bitrate > 512 {
			return errors.New("Opus bitrate must be 32–512 kbps")
		}
		return nil
	}
	for _, rate := range []int{32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320} {
		if rate == e.Bitrate {
			return nil
		}
	}
	return errors.New("unsupported MP3 CBR bitrate")
}

func (e Encoding) Args() []string {
	if e.Codec == "opus" {
		vbr := "on"
		if e.Mode == "cbr" {
			vbr = "off"
		}
		return []string{"-c:a", "libopus", "-b:a", fmt.Sprintf("%dk", e.Bitrate), "-vbr", vbr, "-f", "opus"}
	}
	if e.Mode == "vbr" {
		return []string{"-c:a", "libmp3lame", "-q:a", fmt.Sprint(e.Quality), "-id3v2_version", "4", "-f", "mp3"}
	}
	return []string{"-c:a", "libmp3lame", "-b:a", fmt.Sprintf("%dk", e.Bitrate), "-id3v2_version", "4", "-f", "mp3"}
}

type Settings struct {
	Enabled       bool     `json:"enabled"`
	Source        string   `json:"source"`
	Output        string   `json:"output"`
	Encoding      Encoding `json:"encoding"`
	Concurrency   int      `json:"concurrency"`
	ScanMinutes   int      `json:"scan_minutes"`
	LidarrPrefix  string   `json:"lidarr_prefix"`
	WebhookHash   string   `json:"webhook_hash,omitempty"`
	NavURL        string   `json:"nav_url"`
	NavUser       string   `json:"nav_user"`
	NavPassword   string   `json:"nav_password,omitempty"`
	NavLibrary    int      `json:"nav_library"`
	OIDCIssuer    string   `json:"oidc_issuer"`
	OIDCClientID  string   `json:"oidc_client_id"`
	OIDCSecret    string   `json:"oidc_secret,omitempty"`
	BoundIssuer   string   `json:"bound_issuer,omitempty"`
	BoundSubject  string   `json:"bound_subject,omitempty"`
	BoundUsername string   `json:"bound_username,omitempty"`
	BoundEmail    string   `json:"bound_email,omitempty"`
}

func DefaultSettings() Settings {
	return Settings{Source: "/music/source", Output: "/music/output", Encoding: Encoding{Codec: "opus", Mode: "vbr", Bitrate: 192, Quality: 2}, Concurrency: 1, ScanMinutes: 60, NavLibrary: 1}
}

func (s Settings) Validate() error {
	if err := s.Encoding.Validate(); err != nil {
		return err
	}
	if s.Concurrency < 1 || s.Concurrency > 16 {
		return errors.New("concurrency must be 1–16")
	}
	if s.ScanMinutes < 1 || s.ScanMinutes > 10080 {
		return errors.New("scan interval must be 1–10080 minutes")
	}
	if !filepath.IsAbs(s.Source) || !filepath.IsAbs(s.Output) {
		return errors.New("library paths must be absolute container paths")
	}
	if s.LidarrPrefix != "" && !filepath.IsAbs(s.LidarrPrefix) {
		return errors.New("Lidarr path prefix must be an absolute container path")
	}
	if containsPath(s.Source, s.Output) || containsPath(s.Output, s.Source) {
		return errors.New("source and output roots must not overlap")
	}
	for _, raw := range []string{s.NavURL, s.OIDCIssuer} {
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
			return errors.New("integration URLs must be HTTP(S) URLs without embedded credentials")
		}
	}
	if s.NavURL != "" && (s.NavUser == "" || s.NavPassword == "" || s.NavLibrary < 1) {
		return errors.New("Navidrome requires credentials and a positive library ID")
	}
	if s.OIDCIssuer != "" && (s.OIDCClientID == "" || s.OIDCSecret == "") {
		return errors.New("OIDC requires client ID and secret")
	}
	return nil
}

func containsPath(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Resolve existing ancestors as well as the leaf to reject symlink escapes.
func safePath(root, rel string) (string, error) {
	if rel == "" {
		rel = "."
	}
	if err := validRelativePath(rel); err != nil {
		return "", err
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(base, rel)
	ancestor := path
	for {
		resolved, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			if !containsPath(base, resolved) { return "", errors.New("symlink escapes library root") }
			break
		}
		if !os.IsNotExist(err) { return "", err }
		parent := filepath.Dir(ancestor)
		if parent == ancestor { return "", err }
		ancestor = parent
	}
	if !containsPath(base, path) { return "", errors.New("path escapes library root") }
	return path, nil
}

func validRelativePath(rel string) error {
	if filepath.IsAbs(rel) || !containsPath("/", filepath.Join("/", rel)) || rel == ".." || strings.HasPrefix(filepath.Clean(rel), ".."+string(filepath.Separator)) {
		return errors.New("path escapes library root")
	}
	return nil
}

type Source struct {
	ID            int64             `json:"id"`
	Rel           string            `json:"path"`
	Hash          string            `json:"hash"`
	Size          int64             `json:"size"`
	Mtime         int64             `json:"mtime"`
	Artist        string            `json:"artist"`
	Album         string            `json:"album"`
	Title         string            `json:"title"`
	Track         int               `json:"track"`
	Disc          int               `json:"disc"`
	Duration      float64           `json:"duration"`
	Metadata      map[string]string `json:"metadata"`
	Present       bool              `json:"present"`
	Output        string            `json:"output"`
	BuiltHash     string            `json:"built_hash"`
	BuiltProfile  string            `json:"built_profile"`
	OutputPresent bool              `json:"output_present"`
	Error         string            `json:"error"`
	Status        string            `json:"status"`
}

type Job struct {
	ID       int64           `json:"id"`
	Kind     string          `json:"kind"`
	Key      string          `json:"key"`
	Args     json.RawMessage `json:"args"`
	State    string          `json:"state"`
	Attempts int             `json:"attempts"`
	Progress float64         `json:"progress"`
	Log      string          `json:"log,omitempty"`
	Created  int64           `json:"created"`
	Updated  int64           `json:"updated"`
}

type ScanRequest struct {
	Dirs   []string `json:"dirs"`
	Verify bool     `json:"verify"`
}
type BuildRequest struct {
	ID      int64    `json:"id"`
	Hash    string   `json:"hash"`
	Profile Encoding `json:"profile"`
	Move    bool     `json:"move"`
}
type UpgradeRequest struct {
	New []string `json:"new"`
	Old []string `json:"old"`
}
