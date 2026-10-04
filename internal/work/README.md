# work domain (incl. the discussion sub-domain)

Work-item core and discussion.

- **Responsibilities**: work item CRUD/status catalog/properties/labels/projects/kanban
  moves/table-query compiler/full-text search; comment threads (@mentions/resolve/tombstones);
  six automatic subscription classes; comment-spawned sub work items (capture token/snapshot
  digest/retry); trigger dry-run preview.
- **Spec mapping**: internal archive 05 §3 (table language §3.6, triage §3.9), §4.2 preview
  predicates, §13 sub work items; 06 work/; internal archive 04 §2.3–2.4.
- **Services**: WorkItemService, CommentService, SourceContextService, ViewService, SearchService
  (federated three-way RRF), CatalogService (from M9-B1 including agent/skill label mounting,
  internal archive 05 §2.3 three resource kinds), ClientUsageService (internal archive 04 §2.8
  POST /client-usage, human-exclusive).
- **Ports supplied**: WorkItemReader/WorkBench/TriggerEngine (consumed by orchestration).
- **workers (M9-B1, 06 work/)**: `SearchIndexCatchUp` (ledger catch-up—search is instant
  ILIKE/trgm with no materialization; ruled as orphan-row reconciliation cleanup, rationale in the
  workers/searchindex.go comment) + `PruneMarkSweeper` (retention-window cleanup +
  search_index_prune_mark watermark advance); registration belongs to cmd Workers
  (Name/RunOnce/Run, same shape as notification ArchiveSweeper).
- **Tests**: table trio endpoints, thread folding, subscription cascades, preview==actual, sub work
  item quartet, RRF fusion; M9-B1 observability face (ledger triggers/catch-up/watermark/daily
  usage/mounting) in pgrepo/observability_integration_test.go.
- **M9-B1 cross-domain registrations** (arch/sql_allowlist.yml): WORK-RESOURCE-LABEL-MOUNT
  (agent/agent_to_label/skill/skill_to_label—mount writes atomic with the existence predicate;
  endpoints in orchestration/skills http inject this domain's service via functions),
  WORK-SEARCH-INDEX-LEDGER (search_index_change/search_index_prune_mark—06 assigns both workers
  to work; platform holds zero business SQL), WORK-CLIENT-USAGE (client_usage_daily—internal
  archive 04 §2.8 misc-endpoint carrying ruling sits in this domain, upsert only).
