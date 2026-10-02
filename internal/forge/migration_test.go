package forge

import (
	"database/sql"
	"path/filepath"
	"testing"
)

func TestSchemaOneUpgradePreservesLibraryAndFailedRetryBudget(t *testing.T) {
	a, s := testApp(t)
	if _, err := a.db.Exec("INSERT INTO sources(id,rel,hash,output,built_hash,built_profile,output_present) VALUES(7,'legacy.flac','legacy-hash','legacy.opus','legacy-hash',?,1)", s.Encoding.Fingerprint()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec("INSERT INTO managed(path,source_id,kind) VALUES('legacy.opus',7,'audio'); INSERT INTO jobs(kind,dedup,args,state,attempts,created,updated) VALUES('convert','legacy-target','{\"id\":7}','failed',3,0,0); INSERT INTO dirty_dirs(path,updated) VALUES('.',1700000000); DELETE FROM migrations WHERE version=2; PRAGMA user_version=1;"); err != nil {
		t.Fatal(err)
	}
	cfg, logger, assets := a.cfg, a.logger, a.assets
	a.Close()
	restored, err := New(cfg, logger, "test", assets)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	source, err := restored.source(7)
	if err != nil || source.Output != "legacy.opus" || !source.OutputPresent || source.BuiltHash != "legacy-hash" {
		t.Fatal("migration changed playback state", source, err)
	}
	if err = restored.owned(source.Output, source.ID); err != nil {
		t.Fatal("migration lost artifact ownership", err)
	}
	var state string
	var attempts, version int
	var stamp int64
	if err = restored.db.QueryRow("SELECT state,attempts FROM jobs WHERE dedup='legacy-target'").Scan(&state, &attempts); err != nil || state != "failed" || attempts != 3 {
		t.Fatal("migration reset exhausted job", state, attempts, err)
	}
	if err = restored.db.QueryRow("SELECT updated FROM dirty_dirs WHERE path='.'").Scan(&stamp); err != nil || stamp != 1700000000000000000 {
		t.Fatal("legacy refresh stamp not migrated", stamp, err)
	}
	if err = restored.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 2 {
		t.Fatal("schema version not advanced", version, err)
	}
}

func TestNewerSchemaIsRejectedWithoutDowngrading(t *testing.T) {
	path := filepath.Join(t.TempDir(), "future.db")
	db, err := openDB(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("PRAGMA user_version=3"); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	unexpected, err := openDB(path)
	if err == nil {
		unexpected.Close()
		t.Fatal("accepted unsupported newer schema")
	}
	inspection, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	var version int
	if err = inspection.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 3 {
		t.Fatal("rejection changed future schema", version, err)
	}
}
