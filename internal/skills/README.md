# skills domain

Skill library: storage, import, search and agent binding of SKILL.md skills, plus the data face
for the engine's progressive disclosure.

## Structure and exported face

| Layer | Contents |
|---|---|
| domain/rules.go | Pure decisions: `ParseFrontmatter`, `CleanPath`, `IsBinaryExt`, import-limit constants; `ManifestDigest`/`VerifyManifest` (internal archive 05 §12 length-prefixed SHA-256 tree digest: leaf = SHA256(0x01‖len‖path‖len‖content) in path order, internal = SHA256(0x02‖l‖r) with odd tail self-paired; read-validation sentinel `ErrManifestMismatch`) |
| app/service.go | `SkillService` (Create/Get/List/Search/Delete + five agent-binding methods); `Files` (fail-closed manifest verification on read: stored content vs import-manifest rows, mismatch refused to serve, returns the manifest_sha256 tree digest for executor-side verification); `MainByName` (the verified full SKILL.md text for the progressive-disclosure exit); `ImportService` (ImportZip: path-escape rejection / binary skipping / limits / frontmatter name / redactx.Sanitize on persist (content+paths+name, manifest rows counted by sanitized content) / same-name upsert; Refresh re-fetches per recorded origin) |
| adapters/pgrepo | skill/skill_file/agent_skill tables (the built subset of internal archive 03 §2 skills(4)) |
| adapters/http | skills endpoint group + agent binding routes |

**Ports**: `Repo` (16 methods, consumers declared in this domain's app); the engine-side
progressive-disclosure port `SkillSource` (ListSkills with enabled filter + GetSkill by name for
the full text) is declared in `internal/orchestration/adapters/engine/skill.go`
(consumer = the engine); the composition root `cmd/orkome-server/wiring_skills.go` wires it as an
eino skill.Backend + skill.NewMiddleware (internal archive 09 §14: summary first, expand later).

## Known trade-offs

- **Import sources**: of the archive's three (manual/zip/URL fetch), URL fetch is not done (zip +
  manual in); the four zip-import guard rejections are tested (b18_integration_test.go);
- **Manifest-hash read validation landed (D20)**: the `Files`/`MainByName` read paths are
  fail-closed (domain.ErrManifestMismatch); the composition root's SkillSource.GetSkill still
  reads the repo directly—pending switch to `MainByName` (see WIRING);
- **manifest column not created**: the tree digest is recomputed at read time and served with the
  Files API (for executor-side verification); for persisted comparison, add a
  skill.manifest_sha256 column (see WIRING SQL);
- **skill_to_label labeling face**: the table is in the archive's skills(4) listing; label
  associations not implemented.

## Internal archive section mapping

| Internal archive location | Correspondence |
|---|---|
| 05 §12 skills | all rules of this domain |
| 06 skills/ | directory and port listing |
| 09 §14 progressive disclosure | SkillSource → eino middleware |
| 04 endpoint group | skills CRUD/search/files/bindings |
