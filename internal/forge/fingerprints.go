package forge

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"math/cmplx"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"time"
)

type audioFingerprint struct {
	Version    int      `json:"version"`
	ID         int64    `json:"id"`
	Path       string   `json:"path"`
	Signature  string   `json:"signature"`
	SourceRoot string   `json:"source_root"`
	OutputRoot string   `json:"output_root"`
	Duration   float64  `json:"duration"`
	Codes      []uint32 `json:"codes"`
}

// A spectral landmark per 128 ms: band energy above the frame mean is stable
// across codecs and gain changes. shortcut: sample the first 120 seconds; use
// full-track fingerprints if review candidates need stronger identity evidence.
func spectralFingerprint(pcm []byte) ([]uint32, error) {
	const size = 2048
	if len(pcm) < size*4 {
		return nil, errors.New("Audio is too short for acoustic comparison")
	}
	values := make([]complex128, size)
	codes := []uint32{}
	for offset := 0; offset+size*4 <= len(pcm); offset += 1024 * 4 {
		for i := 0; i < size; i++ {
			sample := math.Float32frombits(binary.LittleEndian.Uint32(pcm[offset+i*4:]))
			if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
				return nil, errors.New("Invalid decoded fingerprint samples")
			}
			values[i] = complex(float64(sample)*(.5-.5*math.Cos(2*math.Pi*float64(i)/float64(size-1))), 0)
		}
		// In-place radix-2 FFT; no additional media dependency or native library.
		for i, j := 1, 0; i < size; i++ {
			bit := size >> 1
			for ; j&bit != 0; bit >>= 1 {
				j ^= bit
			}
			j ^= bit
			if i < j {
				values[i], values[j] = values[j], values[i]
			}
		}
		for width := 2; width <= size; width <<= 1 {
			rotation := cmplx.Exp(complex(0, -2*math.Pi/float64(width)))
			for start := 0; start < size; start += width {
				phase := complex(1, 0)
				for j := 0; j < width/2; j++ {
					x, y := values[start+j], values[start+j+width/2]*phase
					values[start+j], values[start+j+width/2] = x+y, x-y
					phase *= rotation
				}
			}
		}
		var bands [32]float64
		total := 0.0
		for band := range bands {
			low := max(1, int(80*math.Pow(3500.0/80, float64(band)/32)*size/8000))
			high := min(size/2, max(low+1, int(80*math.Pow(3500.0/80, float64(band+1)/32)*size/8000)))
			for bin := low; bin < high; bin++ {
				x := values[bin]
				bands[band] += real(x)*real(x) + imag(x)*imag(x)
			}
			total += bands[band]
		}
		code := uint32(0)
		if total > 1e-5 {
			for band, energy := range bands {
				if energy >= total/32 {
					code |= 1 << band
				}
			}
		}
		codes = append(codes, code)
	}
	active := 0
	for _, code := range codes {
		if code != 0 {
			active++
		}
	}
	if active < 8 {
		return nil, errors.New("Audio has too little non-silent content for acoustic comparison")
	}
	return codes, nil
}

func similarFingerprints(a, b audioFingerprint) bool {
	if math.Abs(a.Duration-b.Duration) > math.Max(2, math.Min(a.Duration, b.Duration)*.01) {
		return false
	}
	minimum := min(len(a.Codes), len(b.Codes))
	if minimum < 8 {
		return false
	}
	for offset := -4; offset <= 4; offset++ {
		count, same, active := 0, 0, 0
		for i, x := range a.Codes {
			j := i + offset
			if j < 0 || j >= len(b.Codes) {
				continue
			}
			y := b.Codes[j]
			count++
			if x == 0 || y == 0 {
				continue
			}
			active++
			union := bits.OnesCount32(x | y)
			if float64(bits.OnesCount32(x^y))/float64(union) <= .25 {
				same++
			}
		}
		if active >= 8 && active*5 >= count*4 && count*5 >= minimum*4 && same*10 >= active*9 {
			return true
		}
	}
	return false
}

func (a *App) fingerprintOutput(ctx context.Context, source Source) (audioFingerprint, bool, error) {
	key := "audio-fingerprint:" + strconv.FormatInt(source.ID, 10)
	s, err := a.settingsContext(ctx)
	if err != nil {
		return audioFingerprint{}, false, err
	}
	var signature string
	err = a.reads.QueryRowContext(ctx, "SELECT signature FROM managed WHERE source_id=? AND path=? AND kind='audio'", source.ID, source.Output).Scan(&signature)
	if err != nil {
		return audioFingerprint{}, false, err
	}
	raw, err := a.metaContext(ctx, key)
	var fingerprint audioFingerprint
	if err == nil && json.Unmarshal([]byte(raw), &fingerprint) == nil && fingerprint.Version == 1 && fingerprint.Signature == signature && fingerprint.Path == source.Output && fingerprint.SourceRoot == s.Source && fingerprint.OutputRoot == s.Output && len(fingerprint.Codes) >= 8 && len(fingerprint.Codes) <= 1000 {
		return fingerprint, false, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fingerprint, false, err
	}
	if err = a.waitOutputMaintenanceCapacity(ctx); err != nil {
		return fingerprint, false, err
	}
	staged, track, err := a.stageOutput(ctx, source.ID, signature)
	if err != nil {
		return fingerprint, false, err
	}
	probe, err := a.probe(ctx, staged.Path)
	if err != nil {
		staged.Close()
		return fingerprint, false, err
	}
	decodeCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var pcm, log boundedLog
	pcm.limit = 120*8000*4 + 4096
	command := exec.CommandContext(decodeCtx, a.cfg.FFmpeg, "-nostdin", "-v", "error", "-xerror", "-i", staged.Path, "-map", "0:a:0", "-t", "120", "-ar", "8000", "-ac", "1", "-c:a", "pcm_f32le", "-f", "f32le", "pipe:1")
	command.Stdout, command.Stderr = &pcm, &log
	err = waitTool(decodeCtx, command, a.sourceSlots)
	staged.Close()
	if err != nil {
		return fingerprint, false, fmt.Errorf("Fingerprint decode failed: %w\n%s", err, log.String())
	}
	if pcm.truncated {
		return fingerprint, false, errors.New("Fingerprint sample limit exceeded")
	}
	codes, err := spectralFingerprint(pcm.Bytes())
	if err != nil {
		return fingerprint, false, err
	}
	fingerprint = audioFingerprint{Version: 1, ID: source.ID, Path: track.Source.Output, Signature: signature, SourceRoot: track.Settings.Source, OutputRoot: track.Settings.Output, Duration: probe.duration(), Codes: codes}
	if err = a.lockFiles(ctx); err != nil {
		return fingerprint, false, err
	}
	defer a.files.Unlock()
	current, err := a.source(source.ID)
	if err != nil {
		return fingerprint, false, err
	}
	settings, err := a.settingsContext(ctx)
	if err != nil {
		return fingerprint, false, err
	}
	var actual string
	err = a.reads.QueryRowContext(ctx, "SELECT signature FROM managed WHERE source_id=? AND path=? AND kind='audio'", source.ID, current.Output).Scan(&actual)
	if err != nil || actual != signature || current.Output != track.Source.Output || settings.Source != track.Settings.Source || settings.Output != track.Settings.Output {
		return fingerprint, false, errAuditionUnavailable
	}
	data, _ := json.Marshal(fingerprint)
	return fingerprint, true, a.setMeta(key, string(data))
}

func (a *App) startFingerprints(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.IDs) < 1 || len(body.IDs) > 1000 {
		apiError(w, 400, errors.New("Select between 1 and 1000 tracks"))
		return
	}
	s, err := a.settingsContext(r.Context())
	if err != nil {
		apiError(w, 500, err)
		return
	}
	if !s.Enabled {
		apiError(w, 409, errors.New("library is disabled"))
		return
	}
	sources := []Source{}
	seen := map[int64]bool{}
	for _, id := range body.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		source, readErr := a.source(id)
		if readErr != nil {
			apiError(w, 400, errors.New("Selected track is no longer indexed"))
			return
		}
		if source.OutputPresent && source.Output != "" {
			sources = append(sources, source)
		}
	}
	err = a.runLibraryTool("fingerprints", len(sources), func(ctx context.Context) {
		fingerprints := []audioFingerprint{}
		for _, source := range sources {
			if ctx.Err() != nil {
				break
			}
			a.toolCurrent("fingerprints", source.Rel)
			fingerprint, updated, err := a.fingerprintOutput(ctx, source)
			if ctx.Err() != nil {
				break
			}
			if err == nil {
				fingerprints = append(fingerprints, fingerprint)
			}
			a.toolProgress("fingerprints", source, updated, err)
		}
		sort.Slice(fingerprints, func(i, j int) bool { return fingerprints[i].Duration < fingerprints[j].Duration })
		clusters := [][]audioFingerprint{}
		for _, fingerprint := range fingerprints {
			if ctx.Err() != nil {
				break
			}
			matched := false
			for i := range clusters {
				if similarFingerprints(clusters[i][0], fingerprint) {
					clusters[i] = append(clusters[i], fingerprint)
					matched = true
					break
				}
			}
			if !matched {
				clusters = append(clusters, []audioFingerprint{fingerprint})
			}
		}
		groups := [][]int64{}
		for _, cluster := range clusters {
			if len(cluster) > 1 {
				ids := []int64{}
				for _, item := range cluster {
					ids = append(ids, item.ID)
				}
				groups = append(groups, ids)
			}
		}
		a.toolMu.Lock()
		a.toolReports["fingerprints"].Groups = groups
		a.toolMu.Unlock()
	})
	if err != nil {
		apiError(w, 409, err)
		return
	}
	respond(w, 202, map[string]bool{"ok": true})
}
