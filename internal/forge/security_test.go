package forge

import (
 "bytes"
 "encoding/json"
 "net/http/httptest"
 "os"
 "path/filepath"
 "testing"
)

func TestOIDCReconfigurationRequiresLocalLoginAndRevokesSessions(t *testing.T) {
 a, s := testApp(t)
 s.OIDCIssuer = "https://issuer.test/"
 s.OIDCClientID = "client"
 s.OIDCSecret = "secret"
 s.BoundIssuer = s.OIDCIssuer
 s.BoundSubject = "subject"
 a.cfg.PublicURL = "https://musicforge.test"
 if err := a.saveSettings(s); err != nil { t.Fatal(err) }
 oidc := httptest.NewRecorder()
 if err := a.newSession(oidc, "oidc"); err != nil { t.Fatal(err) }
 local := httptest.NewRecorder()
 if err := a.newSession(local, "local"); err != nil { t.Fatal(err) }
 body, err := json.Marshal(map[string]any{"enabled":s.Enabled, "source":s.Source, "output":s.Output, "encoding":s.Encoding, "concurrency":s.Concurrency, "scan_minutes":s.ScanMinutes, "nav_library":s.NavLibrary, "oidc_issuer":s.OIDCIssuer, "oidc_client_id":s.OIDCClientID, "unbind_oidc":true})
 if err != nil { t.Fatal(err) }
 for _, test := range []struct{recorder *httptest.ResponseRecorder; code int}{{oidc,403},{local,200}} {
  cookie := test.recorder.Result().Cookies()[0]
  req := httptest.NewRequest("PUT", "/api/settings", bytes.NewReader(body))
  req.AddCookie(cookie)
  req.Header.Set("X-CSRF-Token", digest(cookie.Value+":csrf"))
  w := httptest.NewRecorder()
  a.Handler().ServeHTTP(w, req)
  if w.Code != test.code { t.Fatalf("want %d, got %d: %s", test.code,w.Code,w.Body.String()) }
 }
 req := httptest.NewRequest("GET", "/api/library", nil)
 req.AddCookie(oidc.Result().Cookies()[0])
 w := httptest.NewRecorder()
 a.Handler().ServeHTTP(w,req)
 if w.Code != 401 { t.Fatal("unbound identity kept active session", w.Code) }
 latest, err := a.settings()
 if err != nil || latest.BoundSubject != "" { t.Fatal("binding not cleared",err) }
}

func TestResolvedConfigurationOverlapAndURLBoundaries(t *testing.T) {
 a, s := testApp(t)
 alias := filepath.Join(t.TempDir(),"config-alias")
 if err := os.Symlink(a.cfg.ConfigDir,alias); err != nil { t.Fatal(err) }
 a.cfg.ConfigDir = alias
 s.Output = filepath.Join(alias,"output")
 if err := os.MkdirAll(s.Output,0755); err != nil { t.Fatal(err) }
 if err := a.initializeStorage(s); err == nil { t.Fatal("accepted resolved config overlap") }
 for _, value := range []string{"https://user:secret@example.test", "https://example.test/?token=secret", "https://example.test/#fragment"} {
  t.Setenv("MUSICFORGE_PUBLIC_URL",value)
  if _,err := LoadRuntime(t.TempDir()); err == nil { t.Fatal("accepted unsafe public origin",value) }
  normal := DefaultSettings()
  normal.OIDCIssuer=value
  normal.OIDCClientID="client"
  normal.OIDCSecret="secret"
  if err := normal.Validate(); err == nil { t.Fatal("accepted unsafe integration URL",value) }
 }
}
