package forge

import (
 "context"
 "os"
 "path/filepath"
 "testing"
)

func TestMetadataOnlyFullVerificationAndFailedReplacement(t *testing.T){
 a,s:=testApp(t);path:=makeFLAC(t,a,s,"Album/01.flac","First",false);if err:=a.scan(context.Background(),ScanRequest{});err!=nil{t.Fatal(err)};drain(t,a,true);original,err:=a.sourceRel("Album/01.flac");if err!=nil{t.Fatal(err)};info,err:=os.Stat(path);if err!=nil{t.Fatal(err)};artifact:=filepath.Join(s.Output,original.Output);artifactHash,err:=fileHash(context.Background(),artifact);if err!=nil{t.Fatal(err)}
 makeFLAC(t,a,s,"Album/01.flac","Other",false);updated,err:=os.Stat(path);if err!=nil{t.Fatal(err)};if updated.Size()!=info.Size(){t.Fatal("metadata-only fixture must keep size unchanged")};if err=os.Chtimes(path,info.ModTime(),info.ModTime());err!=nil{t.Fatal(err)}
 if err=a.scan(context.Background(),ScanRequest{});err!=nil{t.Fatal(err)};fast,_:=a.source(original.ID);if fast.Hash!=original.Hash{t.Fatal("fast scan unexpectedly read unchanged stat")};if err=a.scan(context.Background(),ScanRequest{Verify:true});err!=nil{t.Fatal(err)};verified,_:=a.source(original.ID);if verified.Hash==original.Hash||verified.Title!="Other"{t.Fatal("full verification missed metadata-only change")}
 a.cfg.FFmpeg="/bin/false";j,err:=a.claim(true);if err!=nil{t.Fatal(err)};if err=a.execute(context.Background(),j);err==nil{t.Fatal("encoder failure reported success")};afterHash,err:=fileHash(context.Background(),artifact);if err!=nil||afterHash!=artifactHash{t.Fatal("failed conversion removed or changed playable output")};after,_:=a.source(original.ID);if after.BuiltHash!=original.Hash{t.Fatal("failed conversion changed built source hash")}
}
