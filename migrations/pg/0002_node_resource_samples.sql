-- Per-node CPU/memory/disk samples, one row per stats poll — backs the Nodes
-- tab's CPU/Memory history views (backlog #3). Mirrors node_traffic_samples:
-- a raw time series pruned to the same retention as traffic_samples, bucketed
-- on read (see Store.NodeResourceSeries).
CREATE TABLE node_resource_samples (
    at timestamp with time zone DEFAULT now() NOT NULL,
    node_id text NOT NULL,
    cpu_load1 double precision NOT NULL,
    cpu_cores integer NOT NULL,
    mem_used_mb bigint NOT NULL,
    mem_total_mb bigint NOT NULL,
    disk_used_gb bigint NOT NULL,
    disk_total_gb bigint NOT NULL
);

CREATE INDEX node_resource_samples_node_at_idx ON node_resource_samples USING btree (node_id, at);
