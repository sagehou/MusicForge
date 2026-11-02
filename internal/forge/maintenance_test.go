package forge

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestMaintenanceScheduleBoundariesAndTimezone(t *testing.T) {
	o := defaultMaintenanceOptions()
	o.ScheduleEnabled, o.Timezone, o.DayConcurrency = true, "Asia/Shanghai", 1
	for _, test := range []struct { utc string; want int }{
		{"2026-11-02T13:59:00Z",1}, {"2026-11-02T14:00:00Z",4}, {"2026-11-02T22:59:00Z",4}, {"2026-11-02T23:00:00Z",1},
	} {
		now, _ := time.Parse(time.RFC3339,test.utc)
		if got := o.concurrency(4,now); got != test.want { t.Fatalf("%s: %d, want %d",test.utc,got,test.want) }
	}
	o.Start,o.End,o.Timezone,o.DayConcurrency="09:00","17:00","America/New_York",0
	for _, test := range []struct{utc string; want int}{ {"2026-03-09T12:59:00Z",0},{"2026-03-09T13:00:00Z",4},{"2026-03-09T21:00:00Z",0} } {
		now,_ := time.Parse(time.RFC3339,test.utc)
		if got := o.concurrency(4,now); got != test.want { t.Fatal(test,got) }
	}
	o.ScheduleEnabled=false
	if o.concurrency(2,time.Now()) != 2 { t.Fatal("disabled schedule changed capacity") }
	o.ScheduleEnabled=true; o.End=o.Start
	if o.validate() == nil { t.Fatal("accepted an empty build window") }
}

func TestBuildPreviewUsesOnlyIndexAndMatchesRebuildEligibility(t *testing.T) {
	a,s := testApp(t)
	seedLyricsArtifact(t,a,s)
	if _,err := a.db.Exec("UPDATE sources SET built_hash=hash,built_profile=?,duration=120,size=1000000",s.Encoding.Fingerprint()); err != nil { t.Fatal(err) }
	request := lyricsSession(t,a)
	if err := os.Rename(s.Source,s.Source+".offline"); err != nil { t.Fatal(err) }
	if err := os.Rename(s.Output,s.Output+".offline"); err != nil { t.Fatal(err) }
	w := request("POST","/api/library/plan",map[string]any{"all":true})
	var plan buildPlan
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(),&plan) != nil || plan.Tracks != 0 || plan.Skipped != 1 { t.Fatal(w.Code,w.Body.String()) }
	if _,err := a.db.Exec("UPDATE sources SET output_present=0"); err != nil { t.Fatal(err) }
	w=request("POST","/api/library/plan",map[string]any{"ids":[]int{1}})
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(),&plan) != nil || plan.Tracks != 1 || plan.Reasons["missing"] != 1 || plan.InputBytes != 1000000 || plan.OutputBytes != 120*192*125 { t.Fatal(w.Code,w.Body.String()) }
	w=request("POST","/api/library/plan",map[string]any{"ids":[]int{999}})
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(),&plan) != nil || plan.Tracks != 0 || plan.Skipped != 0 { t.Fatal(w.Code,w.Body.String()) }
}

func TestLyricsTimingAndManualImportOutputOnly(t *testing.T) {
	text,err := shiftLRC("[offset:250]\n[00:01.25][00:03.250]Original\n[00:01.25]译文",-100)
	if err != nil || text != "[00:00.900][00:02.900]Original\n[00:00.900]译文\n" { t.Fatal(text,err) }
	if _,err=shiftLRC("[00:01]line",3600001); err == nil { t.Fatal("accepted an excessive delay") }
	a,s := testApp(t)
	source := seedLyricsArtifact(t,a,s)
	request := lyricsSession(t,a)
	if err=os.Rename(s.Source,s.Source+".offline"); err != nil { t.Fatal(err) }
	w:=request("GET","/api/library/1/lyrics/edit",nil)
	var edit struct{Token,Text string}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(),&edit) != nil || edit.Token == "" { t.Fatal(w.Code,w.Body.String()) }
	w=request("PUT","/api/library/1/lyrics/edit",map[string]any{"token":edit.Token,"text":"[00:01.00]我的歌词","delay_ms":350})
	if w.Code != 200 || !strings.Contains(w.Body.String(),"[00:01.350]") { t.Fatal(w.Code,w.Body.String()) }
	ready,err:=a.meta("lyrics-status:1"); if err != nil { t.Fatal(err) }
	// Ready results still skip all file/provider work after manual publication.
	if err=os.Rename(s.Output,s.Output+".offline"); err != nil { t.Fatal(err) }
	status:=a.maintainLyrics(context.Background(),s,source.ID,lyricsBatchOptions{MinScore:99})
	if status.State != "ready" || status.Provider != "source" { t.Fatal(status) }
	after,_:=a.meta("lyrics-status:1"); if ready != after { t.Fatal("ordinary filling rewrote a manual ready result") }
	if w=request("PUT","/api/library/1/lyrics/edit",map[string]any{"token":edit.Token,"text":"[00:01]again"}); w.Code != 409 { t.Fatal("reused edit token",w.Code) }
}

func TestLyricsEditorPreservesConcurrentOutput(t *testing.T) {
	a,s:=testApp(t); source:=seedLyricsArtifact(t,a,s); request:=lyricsSession(t,a)
	w:=request("GET","/api/library/1/lyrics/edit",nil); var edit struct{Token string}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(),&edit) != nil { t.Fatal(w.Code,w.Body.String()) }
	if err:=a.writeLyricsFile(context.Background(),s,source,"[00:02]Concurrent choice","source"); err != nil { t.Fatal(err) }
	w=request("PUT","/api/library/1/lyrics/edit",map[string]any{"token":edit.Token,"text":"[00:01]old draft"})
	if w.Code != 409 { t.Fatal(w.Code,w.Body.String()) }
	text,err:=os.ReadFile(filepath.Join(s.Output,"Album/01.lrc")); if err != nil || !strings.Contains(string(text),"Concurrent choice") { t.Fatal(string(text),err) }
}

func TestArtworkOverrideOwnershipAndSourcePreservation(t *testing.T) {
	a,s:=testApp(t); seedLyricsArtifact(t,a,s); request:=lyricsSession(t,a)
	path:=filepath.Join(s.Source,"Album/cover.jpg"); writeCover(t,path)
	before,err:=os.ReadFile(path); if err != nil { t.Fatal(err) }
	w:=request("GET","/api/library/1/artwork/edit",nil); var edit struct{Token string}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(),&edit) != nil { t.Fatal(w.Code,w.Body.String()) }
	w=request("PUT","/api/library/1/artwork/edit",map[string]any{"token":edit.Token,"image":base64.StdEncoding.EncodeToString(before)})
	if w.Code != 200 { t.Fatal(w.Code,w.Body.String()) }
	after,err:=os.ReadFile(path); if err != nil || string(after)!=string(before) { t.Fatal("source artwork changed",err) }
	input,signature,override,err:=a.overrideArtwork(context.Background(),"Album"); if err != nil || !override || input=="" || signature=="" { t.Fatal(input,signature,override,err) }
	output:=filepath.Join(s.Output,"Album/cover.jpg"); published,err:=os.ReadFile(output); if err != nil { t.Fatal(err) }
	if err=os.Rename(s.Source,s.Source+".offline"); err != nil { t.Fatal(err) }
	if err=a.artwork(context.Background(),s,"Album",nil); err != nil { t.Fatal("override needed source storage",err) }
	w=request("GET","/api/library/1/artwork/edit",nil); if w.Code != 200 || json.Unmarshal(w.Body.Bytes(),&edit)!=nil { t.Fatal(w.Code,w.Body.String()) }
	if err=os.WriteFile(output,[]byte("external edit"),0644); err != nil { t.Fatal(err) }
	w=request("PUT","/api/library/1/artwork/edit",map[string]any{"token":edit.Token,"image":base64.StdEncoding.EncodeToString(before)})
	if w.Code != 409 { t.Fatal("overwrote concurrent cover",w.Code,w.Body.String()) }
	if len(published)==0 { t.Fatal("empty published cover") }
}

func TestNotificationsDeduplicateAndNeverFollowRedirects(t *testing.T) {
	a,s:=testApp(t); options:=defaultMaintenanceOptions(); var calls atomic.Int32
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		calls.Add(1); if r.Header.Get("Authorization")!="Bearer private-token" { t.Error("missing authorization") }; _,_=io.Copy(io.Discard,r.Body); w.WriteHeader(204)
	})); defer server.Close()
	options.NotifyURL,options.NotifyToken=server.URL,"private-token"; raw,_:=json.Marshal(options)
	if err:=a.setMeta(maintenanceOptionsKey,string(raw)); err != nil { t.Fatal(err) }
	if _,err:=a.db.Exec("INSERT INTO jobs(kind,dedup,args,state,attempts,created,updated) VALUES('convert','test:failed','{}','failed',3,1,1)"); err != nil { t.Fatal(err) }
	a.notificationTick(context.Background(),s); a.notifyNext=time.Time{}; a.notificationTick(context.Background(),s)
	if calls.Load()!=1 { t.Fatal("duplicate delivery",calls.Load()) }
	request:=lyricsSession(t,a); w:=request("GET","/api/settings",nil)
	if w.Code!=200 || strings.Contains(w.Body.String(),"private-token") { t.Fatal("settings exposed notification credentials",w.Code) }
	redirect:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){ http.Redirect(w,r,server.URL,302) })); defer redirect.Close()
	options.NotifyURL=redirect.URL
	if err:=sendNotification(context.Background(),options,"test","test"); err==nil || strings.Contains(err.Error(),options.NotifyToken) { t.Fatal(err) }
	if calls.Load()!=1 { t.Fatal("followed notification redirect") }
}
