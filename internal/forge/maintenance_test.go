package forge

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
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

func TestNewTrackPriorityAndReducedCapacity(t *testing.T) {
	a,s:=testApp(t)
	if _,err:=a.db.Exec(`INSERT INTO sources(id,rel,hash,output_present) VALUES(1,'Album/old.flac','old',1),(2,'Album/new.flac','new',0)`); err!=nil { t.Fatal(err) }
	oldID,err:=a.queueBuild(Source{ID:1,Hash:"old"},s.Encoding,false,true); if err!=nil { t.Fatal(err) }
	newID,err:=a.queueBuild(Source{ID:2,Hash:"new"},s.Encoding,false,true); if err!=nil { t.Fatal(err) }
	o:=defaultMaintenanceOptions(); o.PrioritizeNew=true; raw,_:=json.Marshal(o)
	if err=a.setMeta(maintenanceOptionsKey,string(raw)); err!=nil { t.Fatal(err) }
	job,err:=a.claimWithRepairCapacity(true,2); if err!=nil || job.ID!=newID { t.Fatal("new output was not prioritized",job,err) }
	if _,err=a.claimWithRepairCapacity(true,1); err!=sql.ErrNoRows { t.Fatal("reduced capacity admitted another job",err) }
	if _,err=a.db.Exec("UPDATE jobs SET state='success' WHERE id=?",newID); err!=nil { t.Fatal(err) }
	a.loudnessBusy.Store(true)
	if _,err=a.claimWithRepairCapacity(true,1); err!=sql.ErrNoRows { t.Fatal("loudness reservation exceeded concurrency",err) }
	a.loudnessBusy.Store(false)
	job,err=a.claimWithRepairCapacity(true,1); if err!=nil || job.ID!=oldID { t.Fatal(job,err) }
}

func TestMaintenanceSettingsTokenPreservationAndAtomicValidation(t *testing.T) {
	a,s:=testApp(t); request:=lyricsSession(t,a)
	options:=defaultMaintenanceOptions(); options.NotifyURL,options.NotifyToken="https://ntfy.example.com/topic","private-token"
	raw,_:=json.Marshal(options); if err:=a.setMeta(maintenanceOptionsKey,string(raw)); err!=nil { t.Fatal(err) }
	body:=map[string]any{}; raw,_=json.Marshal(s); if err:=json.Unmarshal(raw,&body); err!=nil { t.Fatal(err) }
	options.NotifyToken=""; options.PrioritizeNew=true; body["maintenance"]=options
	w:=request("PUT","/api/settings",body); if w.Code!=200 { t.Fatal(w.Code,w.Body.String()) }
	saved,err:=a.maintenanceOptions(context.Background()); if err!=nil || saved.NotifyToken!="private-token" || !saved.PrioritizeNew { t.Fatal(saved,err) }
	options.Timezone="invalid/timezone"; body["maintenance"]=options; body["concurrency"]=4
	w=request("PUT","/api/settings",body); if w.Code!=400 { t.Fatal(w.Code,w.Body.String()) }
	unchanged,err:=a.settings(); if err!=nil || unchanged.Concurrency!=s.Concurrency { t.Fatal("invalid maintenance saved partial settings",unchanged,err) }
	options.Timezone="UTC"; options.NotifyURL="https://ntfy.example.com/other"; body["maintenance"]=options
	w=request("PUT","/api/settings",body); if w.Code!=200 { t.Fatal(w.Code,w.Body.String()) }
	saved,err=a.maintenanceOptions(context.Background()); if err!=nil || saved.NotifyToken!="" { t.Fatal("forwarded saved token to changed endpoint",saved,err) }
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
	w=request("PUT","/api/lyrics/options",map[string]any{"auto_search":false,"min_score":99})
	if w.Code!=200 { t.Fatal(w.Code,w.Body.String()) }
	w=request("POST","/api/lyrics/search",map[string]any{"ids":[]int64{source.ID}})
	if w.Code!=202 { t.Fatal(w.Code,w.Body.String()) }
	var skipped struct{Selected,Skipped int}; if json.Unmarshal(w.Body.Bytes(),&skipped)!=nil || skipped.Selected!=0 || skipped.Skipped!=1 { t.Fatal(w.Body.String()) }
	waitLyrics(t,a)
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

func TestNotificationOfflineThresholdWithoutDashboardPolling(t *testing.T) {
	a,s:=testApp(t); var calls atomic.Int32
	server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){ calls.Add(1); w.WriteHeader(204) })); defer server.Close()
	o:=defaultMaintenanceOptions(); o.NotifyURL=server.URL; raw,_:=json.Marshal(o)
	if err:=a.setMeta(maintenanceOptionsKey,string(raw)); err!=nil { t.Fatal(err) }
	a.storageMu.Lock(); a.storageSource,a.storageOutput=s.Source,s.Output; a.storageCheck=true; a.storageAt=time.Now(); a.storageError=errors.New("offline"); a.storageMu.Unlock()
	a.notificationTick(context.Background(),s)
	if calls.Load()!=0 || a.offlineSince.IsZero() { t.Fatal("outage was not tracked before the notification interval",calls.Load()) }
	a.offlineSince=time.Now().Add(-6*time.Minute); a.notifyNext=time.Time{}
	a.notificationTick(context.Background(),s); a.notifyNext=time.Time{}; a.notificationTick(context.Background(),s)
	if calls.Load()!=1 { t.Fatal("offline threshold or deduplication failed",calls.Load()) }
	a.storageMu.Lock(); a.storageError=nil; a.storageMu.Unlock()
	a.notificationTick(context.Background(),s)
	if !a.offlineSince.IsZero() { t.Fatal("online recovery did not reset the outage while delivery was throttled") }
}
