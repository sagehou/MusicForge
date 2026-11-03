package forge

import (
	"context"
	"errors"
	"io"
	"os"
	"net/http"
	"path/filepath"
	"syscall"
	"time"
)

type storageProbe struct {
	Path string `json:"path"`
	Total uint64 `json:"total_bytes"`
	Free uint64 `json:"free_bytes"`
	Empty bool `json:"empty"`
	Marker string `json:"marker,omitempty"`
}

// Runs in the disposable I/O process so a stalled filesystem cannot hold HTTP handlers.
func inspectStorageDirectory(path string) (storageProbe, error) {
	probe := storageProbe{Path:path}
	var disk syscall.Statfs_t
	if err := syscall.Statfs(path,&disk); err != nil { return probe,err }
	probe.Total,probe.Free=uint64(disk.Blocks)*uint64(disk.Bsize),uint64(disk.Bavail)*uint64(disk.Bsize)
	dir,err:=os.Open(path); if err!=nil{return probe,err}
	names,readErr:=dir.Readdirnames(1); closeErr:=dir.Close()
	if readErr!=nil && !errors.Is(readErr,io.EOF) { return probe,readErr }; if closeErr!=nil{return probe,closeErr}
	probe.Empty=len(names)==0
	marker:=filepath.Join(path,".musicforge")
	if info,statErr:=os.Lstat(marker); statErr==nil {
		if !info.Mode().IsRegular() || info.Size()>1024 {return probe,errors.New("Invalid output ownership marker")}
		raw,readErr:=os.ReadFile(marker);if readErr!=nil{return probe,readErr};probe.Marker=string(raw)
	} else if !os.IsNotExist(statErr) {return probe,statErr}
	return probe,nil
}

func (a *App) indexedStorageTotals(ctx context.Context) (int64,int64,int64,int64,error) {
	sources,err:=a.sourcesWhereContext(ctx,"");if err==nil{err=a.annotateMedia(ctx,sources)}
	var outputs,sourceBytes,outputBytes int64
	for _,source:=range sources {if source.Present{sourceBytes+=source.Size};if source.OutputPresent{outputs++;if source.OutputSize!=nil{outputBytes+=*source.OutputSize}}}
	return int64(len(sources)),outputs,sourceBytes,outputBytes,err
}

func (a *App) probeStorage(ctx context.Context, root string) (storageProbe, error) {
	var probe storageProbe
	err := a.sourceIO(ctx, "storage", root, ".", func(event sourceEvent) error {
		if event.Storage != nil { probe = *event.Storage }; return nil
	})
	return probe, err
}

func (a *App) getStorageHealth(w http.ResponseWriter, r *http.Request) {
	s, err := a.settingsContext(r.Context())
	if err != nil { apiError(w, 500, err); return }
	online, message, checking := a.storageStatus(s)
	tracks,outputs,_,outputBytes,err := a.indexedStorageTotals(r.Context())
	if err != nil { apiError(w, 500, err); return }
	a.stagingMu.Lock(); staging := a.stagingBytes; a.stagingMu.Unlock()
	respond(w, 200, map[string]any{"source":s.Source, "output":s.Output, "config":a.cfg.ConfigDir, "online":online, "checking":checking, "message":message, "tracks":tracks, "outputs":outputs, "output_bytes":outputBytes, "staging_bytes":staging, "staging_limit_bytes":a.cfg.StagingMaxBytes})
}

func (a *App) checkStorageHealth(w http.ResponseWriter, r *http.Request) {
	s, err := a.settingsContext(r.Context())
	if err != nil { apiError(w, 500, err); return }
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second); defer cancel()
	result := map[string]any{}
	for _, item := range []struct{kind, path string}{{"config",a.cfg.ConfigDir},{"output",s.Output},{"staging",a.stagingDir}} {
		probe, checkErr := a.probeStorage(ctx, item.path)
		if checkErr != nil { result[item.kind] = map[string]string{"error":checkErr.Error()} } else { result[item.kind] = probe }
	}
	info, sourceErr := a.sourceStat(ctx, s.Source, ".")
	if sourceErr == nil { sourceErr = a.verifySourceRoot(info) }
	if sourceErr != nil { result["source"] = map[string]string{"error":sourceErr.Error()} } else { result["source"] = map[string]string{"path":info.Path, "identity":info.Identity} }
	if err = a.verifyOutputStorage(ctx, s); err != nil { result["ownership_error"] = err.Error() }
	respond(w, 200, result)
}

func (a *App) previewMigration(w http.ResponseWriter, r *http.Request) {
	var body struct { Source string `json:"source"`; Output string `json:"output"` }
	if !decode(w, r, &body) { return }
	s, err := a.settingsContext(r.Context())
	if err != nil { apiError(w, 500, err); return }
	candidate := s; candidate.Source, candidate.Output = filepath.Clean(body.Source), filepath.Clean(body.Output)
	if err = candidate.Validate(); err != nil { apiError(w, 400, err); return }
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second); defer cancel()
	source, err := a.sourceStat(ctx, candidate.Source, ".")
	if err != nil || !source.IsDir() { apiError(w, 409, errors.New("Migration source must be an accessible mounted directory")); return }
	output, err := a.probeStorage(ctx, candidate.Output)
	if err != nil { apiError(w, 409, err); return }
	config, err := a.sourceStat(ctx, a.cfg.ConfigDir, ".")
	if err != nil { apiError(w, 409, err); return }
	if containsPath(source.Path, output.Path) || containsPath(output.Path, source.Path) || containsPath(config.Path, source.Path) || containsPath(config.Path, output.Path) || containsPath(source.Path, config.Path) || containsPath(output.Path, config.Path) {
		apiError(w, 400, errors.New("Resolved migration paths and configuration must not overlap")); return
	}
	instance, err := a.metaContext(ctx, "instance")
	if err != nil { apiError(w, 500, err); return }
	if !output.Empty && output.Marker != instance { apiError(w, 409, errors.New("Migration output must be empty or belong to this MusicForge instance")); return }
	_,_,sourceBytes,outputBytes,err := a.indexedStorageTotals(ctx)
	if err != nil { apiError(w,500,err);return }
	respond(w, 200, map[string]any{"source":source.Path, "output":output, "source_bytes":sourceBytes, "output_bytes":outputBytes, "same_source_path":candidate.Source == s.Source, "same_output_path":candidate.Output == s.Output, "paths_applied":false})
}

func (a *App) startStorageVerification(w http.ResponseWriter, r *http.Request) {
	var body struct { IDs []int64 `json:"ids"` }
	if !decode(w, r, &body) { return }
	if len(body.IDs) == 0 || len(body.IDs)>1000 { apiError(w, 400, errors.New("Select between 1 and 1000 tracks")); return }
	s, err := a.settingsContext(r.Context())
	if err != nil { apiError(w, 500, err); return }
	sources := []Source{}; seen := map[int64]bool{}
	for _, id := range body.IDs {
		if seen[id] { continue }; seen[id] = true
		source, readErr := a.source(id)
		if readErr != nil { apiError(w, 409, readErr); return }
		if source.OutputPresent { sources = append(sources, source) }
	}
	err = a.runLibraryTool("storage",len(sources),func(ctx context.Context) {
		for _, source := range sources {
			if ctx.Err()!=nil { break }
			a.toolCurrent("storage",source.Output)
			checkErr := a.verifyOutputStorage(ctx,s)
			var expected string
			if checkErr == nil { checkErr = a.reads.QueryRowContext(ctx,"SELECT signature FROM managed WHERE source_id=? AND path=? AND kind='audio'",source.ID,source.Output).Scan(&expected) }
			var actual string
			if checkErr == nil { actual,checkErr = a.sourceHash(ctx,s.Output,source.Output,nil) }
			if ctx.Err()!=nil { break }
			if checkErr == nil {
				var current string
				checkErr = a.reads.QueryRowContext(ctx,"SELECT signature FROM managed WHERE source_id=? AND path=? AND kind='audio'",source.ID,source.Output).Scan(&current)
				settings, settingsErr := a.settingsContext(ctx)
				if settingsErr!=nil { checkErr=settingsErr } else if settings.Source!=s.Source || settings.Output!=s.Output || current!=expected { checkErr=errAuditionUnavailable } else if actual!=expected { checkErr=errors.New("Output checksum differs from its registered signature; file preserved") }
			}
			a.toolProgress("storage",source,checkErr==nil,checkErr)
		}
	})
	if err != nil { apiError(w,409,err);return }; respond(w,202,map[string]bool{"ok":true})
}
