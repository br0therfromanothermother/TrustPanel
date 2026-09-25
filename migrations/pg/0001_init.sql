-- TrustPanel schema. Baseline for a fresh install.
--
BEGIN;

-- A group's default_exit_id may hold the sentinel 'direct', which pins the group
-- to local egress and is not a node id, so the reference cannot sit under a
-- plain foreign key. Two triggers keep the same guarantees instead: a
-- non-sentinel value must name a real node, and deleting a node clears the
-- groups that pointed at it.
CREATE FUNCTION groups_default_exit_check() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    IF NEW.default_exit_id IS NOT NULL
       AND NEW.default_exit_id <> 'direct'
       AND NOT EXISTS (SELECT 1 FROM nodes WHERE id = NEW.default_exit_id) THEN
        RAISE EXCEPTION 'default_exit_id % is not a node', NEW.default_exit_id
            USING ERRCODE = 'foreign_key_violation',
                  CONSTRAINT = 'groups_default_exit_id_fkey';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION nodes_clear_group_default() RETURNS trigger
    LANGUAGE plpgsql
    AS $$
BEGIN
    UPDATE groups SET default_exit_id = NULL, updated_at = now()
    WHERE default_exit_id = OLD.id;
    RETURN OLD;
END;
$$;

-- One-time codes that let an operator who lost the panel password prove control
-- of the bound Telegram account. The bot issues a code and the panel redeems it;
-- they are separate processes sharing only this database. Only the SHA-256 is
-- stored, so reading this table does not yield a usable code.
CREATE TABLE admin_recovery_codes (
    code_hash text NOT NULL,
    username text NOT NULL,
    issued_to bigint,
    expires_at timestamp with time zone NOT NULL,
    used_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE admins (
    username text NOT NULL,
    password_hash text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    role text DEFAULT 'admin'::text NOT NULL,
    telegram_id bigint,
    alert_chat_id text DEFAULT ''::text NOT NULL,
    namespace text DEFAULT ''::text NOT NULL,
    locale text DEFAULT ''::text NOT NULL,
    CONSTRAINT admins_role_check CHECK ((role = ANY (ARRAY['admin'::text, 'operator'::text])))
);

CREATE TABLE alert_heartbeat (
    id boolean DEFAULT true NOT NULL,
    ok_at timestamp with time zone,
    CONSTRAINT alert_heartbeat_id_check CHECK (id)
);

CREATE TABLE control_plane (
    id boolean DEFAULT true NOT NULL,
    active_node_id text,
    epoch bigint DEFAULT 0 NOT NULL,
    standby_node_ids text[] DEFAULT '{}'::text[] NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT control_plane_id_check CHECK (id)
);

-- Singleton row: Promote() does UPDATE ... RETURNING with no upsert fallback,
-- so this must exist before the first bootstrap.
INSERT INTO control_plane (id) VALUES (true);

CREATE TABLE domains (
    id text NOT NULL,
    hostname text NOT NULL,
    purpose text NOT NULL,
    node_id text NOT NULL,
    tls_status text DEFAULT 'pending'::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    tls_issuer text DEFAULT ''::text NOT NULL,
    CONSTRAINT domains_purpose_check CHECK ((purpose = ANY (ARRAY['endpoint'::text, 'decoy'::text])))
);

CREATE TABLE events (
    id bigint NOT NULL,
    at timestamp with time zone DEFAULT now() NOT NULL,
    kind text NOT NULL,
    severity text NOT NULL,
    message text NOT NULL,
    actor text DEFAULT ''::text NOT NULL,
    owner_id text DEFAULT ''::text NOT NULL,
    message_ru text DEFAULT ''::text NOT NULL,
    op_id text DEFAULT ''::text NOT NULL
);

CREATE SEQUENCE events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

ALTER SEQUENCE events_id_seq OWNED BY events.id;

CREATE TABLE groups (
    id text NOT NULL,
    name text NOT NULL,
    default_exit_id text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    owner_id text
);

CREATE TABLE namespaces (
    id text NOT NULL,
    label text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);

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

CREATE TABLE node_traffic (
    node_id text NOT NULL,
    period text DEFAULT ''::text NOT NULL,
    rx_bytes bigint DEFAULT 0 NOT NULL,
    tx_bytes bigint DEFAULT 0 NOT NULL,
    last_abs_rx bigint DEFAULT 0 NOT NULL,
    last_abs_tx bigint DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE node_traffic_samples (
    at timestamp with time zone DEFAULT now() NOT NULL,
    node_id text NOT NULL,
    rx_bytes bigint NOT NULL,
    tx_bytes bigint NOT NULL
);

CREATE TABLE nodes (
    id text NOT NULL,
    name text NOT NULL,
    public_role text NOT NULL,
    mgmt_capable boolean DEFAULT false NOT NULL,
    public_ips text[] DEFAULT '{}'::text[] NOT NULL,
    agent_addr text DEFAULT ''::text NOT NULL,
    health text DEFAULT 'unknown'::text NOT NULL,
    last_seen_at timestamp with time zone,
    pg_role text DEFAULT 'none'::text NOT NULL,
    dial_in jsonb,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    limits jsonb,
    billing jsonb,
    managed_services jsonb,
    owner_id text,
    maintenance boolean DEFAULT false NOT NULL,
    CONSTRAINT dial_in_role CHECK ((((public_role = 'exit'::text) AND (dial_in IS NOT NULL)) OR ((public_role = 'entry'::text) AND (dial_in IS NULL)))),
    CONSTRAINT nodes_pg_role_check CHECK ((pg_role = ANY (ARRAY['none'::text, 'primary'::text, 'replica'::text]))),
    CONSTRAINT nodes_public_role_check CHECK ((public_role = ANY (ARRAY['entry'::text, 'exit'::text])))
);

CREATE TABLE route_policies (
    id text NOT NULL,
    name text DEFAULT ''::text NOT NULL,
    priority integer DEFAULT 0 NOT NULL,
    tier text NOT NULL,
    applies_to_group_id text,
    match_domains text[] DEFAULT '{}'::text[] NOT NULL,
    match_cidrs text[] DEFAULT '{}'::text[] NOT NULL,
    match_geoip text[] DEFAULT '{}'::text[] NOT NULL,
    match_geosite text[] DEFAULT '{}'::text[] NOT NULL,
    action text NOT NULL,
    exit_node_id text,
    fallback_kind text,
    fallback_exit_id text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    exclude_domains text[] DEFAULT '{}'::text[] NOT NULL,
    disabled boolean DEFAULT false NOT NULL,
    owner_id text,
    conditions jsonb DEFAULT '[]'::jsonb NOT NULL,
    others_action text DEFAULT ''::text NOT NULL,
    others_exit_id text,
    CONSTRAINT exit_needs_node CHECK (((action <> 'exit'::text) OR (exit_node_id IS NOT NULL))),
    CONSTRAINT others_exit_needs_node CHECK (((others_action <> 'exit'::text) OR (others_exit_id IS NOT NULL))),
    CONSTRAINT route_policies_action_check CHECK ((action = ANY (ARRAY['exit'::text, 'direct'::text, 'block'::text]))),
    CONSTRAINT route_policies_fallback_kind_check CHECK (((fallback_kind IS NULL) OR (fallback_kind = ANY (ARRAY['block'::text, 'direct'::text, 'exit'::text])))),
    CONSTRAINT route_policies_tier_check CHECK ((tier = ANY (ARRAY['fleet'::text, 'exit'::text])))
);

CREATE TABLE rule_set_sources (
    kind text NOT NULL,
    preset text NOT NULL,
    url text NOT NULL,
    checked_at timestamp with time zone,
    check_error text DEFAULT ''::text NOT NULL,
    catalog text[] DEFAULT '{}'::text[] NOT NULL,
    catalog_prev text[] DEFAULT '{}'::text[] NOT NULL,
    catalog_at timestamp with time zone
);

-- The geoip/geosite copies routing is built from. Each row also carries the
-- generation before the current one, so an update can be undone without going
-- back to the source. One generation deep: the question after a bad update is
-- always "put back what worked an hour ago".
CREATE TABLE rule_sets (
    tag text NOT NULL,
    kind text NOT NULL,
    size bigint DEFAULT 0 NOT NULL,
    content_id text DEFAULT ''::text NOT NULL,
    fetched_at timestamp with time zone,
    upstream_id text DEFAULT ''::text NOT NULL,
    upstream_size bigint DEFAULT 0 NOT NULL,
    checked_at timestamp with time zone,
    upstream_gone boolean DEFAULT false NOT NULL,
    auto_update boolean DEFAULT false NOT NULL,
    prev_size bigint DEFAULT 0 NOT NULL,
    prev_content_id text DEFAULT ''::text NOT NULL,
    prev_fetched_at timestamp with time zone
);

CREATE TABLE settings (
    id boolean DEFAULT true NOT NULL,
    data jsonb DEFAULT '{}'::jsonb NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT settings_id_check CHECK (id)
);

CREATE TABLE traffic_samples (
    at timestamp with time zone DEFAULT now() NOT NULL,
    user_id text NOT NULL,
    entry_node_id text NOT NULL,
    rx_bytes bigint NOT NULL,
    tx_bytes bigint NOT NULL
);

CREATE TABLE user_traffic (
    user_id text NOT NULL,
    entry_node_id text NOT NULL,
    rx_bytes bigint DEFAULT 0 NOT NULL,
    tx_bytes bigint DEFAULT 0 NOT NULL,
    last_abs_rx bigint DEFAULT 0 NOT NULL,
    last_abs_tx bigint DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);

CREATE TABLE users (
    id text NOT NULL,
    username text NOT NULL,
    secret text DEFAULT ''::text NOT NULL,
    display_name text DEFAULT ''::text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    group_id text NOT NULL,
    expires_at timestamp with time zone,
    data_limit bigint DEFAULT 0 NOT NULL,
    used_traffic bigint DEFAULT 0 NOT NULL,
    reset_period text DEFAULT ''::text NOT NULL,
    used_reset_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    owner_id text,
    expiry_alerted_for timestamp with time zone
);

ALTER TABLE ONLY events ALTER COLUMN id SET DEFAULT nextval('events_id_seq'::regclass);

ALTER TABLE ONLY admin_recovery_codes
    ADD CONSTRAINT admin_recovery_codes_pkey PRIMARY KEY (code_hash);

ALTER TABLE ONLY admins
    ADD CONSTRAINT admins_pkey PRIMARY KEY (username);

ALTER TABLE ONLY alert_heartbeat
    ADD CONSTRAINT alert_heartbeat_pkey PRIMARY KEY (id);

ALTER TABLE ONLY control_plane
    ADD CONSTRAINT control_plane_pkey PRIMARY KEY (id);

ALTER TABLE ONLY domains
    ADD CONSTRAINT domains_hostname_key UNIQUE (hostname);

ALTER TABLE ONLY domains
    ADD CONSTRAINT domains_pkey PRIMARY KEY (id);

ALTER TABLE ONLY events
    ADD CONSTRAINT events_pkey PRIMARY KEY (id);

ALTER TABLE ONLY groups
    ADD CONSTRAINT groups_pkey PRIMARY KEY (id);

ALTER TABLE ONLY namespaces
    ADD CONSTRAINT namespaces_pkey PRIMARY KEY (id);

ALTER TABLE ONLY node_traffic
    ADD CONSTRAINT node_traffic_pkey PRIMARY KEY (node_id);

ALTER TABLE ONLY nodes
    ADD CONSTRAINT nodes_pkey PRIMARY KEY (id);

ALTER TABLE ONLY route_policies
    ADD CONSTRAINT route_policies_pkey PRIMARY KEY (id);

ALTER TABLE ONLY rule_set_sources
    ADD CONSTRAINT rule_set_sources_pkey PRIMARY KEY (kind);

ALTER TABLE ONLY rule_sets
    ADD CONSTRAINT rule_sets_pkey PRIMARY KEY (tag);

ALTER TABLE ONLY settings
    ADD CONSTRAINT settings_pkey PRIMARY KEY (id);

ALTER TABLE ONLY user_traffic
    ADD CONSTRAINT user_traffic_pkey PRIMARY KEY (user_id, entry_node_id);

ALTER TABLE ONLY users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);

ALTER TABLE ONLY users
    ADD CONSTRAINT users_username_key UNIQUE (username);

CREATE INDEX admin_recovery_codes_username ON admin_recovery_codes USING btree (username);

CREATE UNIQUE INDEX admins_telegram_id_idx ON admins USING btree (telegram_id) WHERE (telegram_id IS NOT NULL);

CREATE INDEX events_at_idx ON events USING btree (at DESC);

CREATE INDEX events_op_id_idx ON events USING btree (op_id) WHERE (op_id <> ''::text);

CREATE INDEX groups_owner_id_idx ON groups USING btree (owner_id);

CREATE INDEX node_resource_samples_node_at_idx ON node_resource_samples USING btree (node_id, at);

CREATE INDEX node_traffic_samples_node_at_idx ON node_traffic_samples USING btree (node_id, at);

CREATE INDEX nodes_owner_id_idx ON nodes USING btree (owner_id);

CREATE INDEX route_policies_owner_id_idx ON route_policies USING btree (owner_id);

CREATE INDEX route_policies_priority_idx ON route_policies USING btree (priority DESC);

CREATE INDEX rule_sets_kind_idx ON rule_sets USING btree (kind);

CREATE INDEX traffic_samples_at_idx ON traffic_samples USING btree (at);

CREATE INDEX traffic_samples_user_at_idx ON traffic_samples USING btree (user_id, at);

CREATE INDEX users_group_id_idx ON users USING btree (group_id);

CREATE INDEX users_owner_id_idx ON users USING btree (owner_id);

CREATE TRIGGER groups_default_exit_check BEFORE INSERT OR UPDATE ON groups FOR EACH ROW EXECUTE FUNCTION groups_default_exit_check();

CREATE TRIGGER nodes_clear_group_default BEFORE DELETE ON nodes FOR EACH ROW EXECUTE FUNCTION nodes_clear_group_default();

ALTER TABLE ONLY admin_recovery_codes
    ADD CONSTRAINT admin_recovery_codes_username_fkey FOREIGN KEY (username) REFERENCES admins(username) ON DELETE CASCADE;

ALTER TABLE ONLY control_plane
    ADD CONSTRAINT control_plane_active_node_id_fkey FOREIGN KEY (active_node_id) REFERENCES nodes(id) ON DELETE SET NULL;

ALTER TABLE ONLY domains
    ADD CONSTRAINT domains_node_id_fkey FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE;

ALTER TABLE ONLY groups
    ADD CONSTRAINT groups_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES namespaces(id) ON DELETE RESTRICT;

ALTER TABLE ONLY node_traffic
    ADD CONSTRAINT node_traffic_node_id_fkey FOREIGN KEY (node_id) REFERENCES nodes(id) ON DELETE CASCADE;

ALTER TABLE ONLY nodes
    ADD CONSTRAINT nodes_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES namespaces(id) ON DELETE RESTRICT;

ALTER TABLE ONLY route_policies
    ADD CONSTRAINT route_policies_applies_to_group_id_fkey FOREIGN KEY (applies_to_group_id) REFERENCES groups(id) ON DELETE CASCADE;

ALTER TABLE ONLY route_policies
    ADD CONSTRAINT route_policies_exit_node_id_fkey FOREIGN KEY (exit_node_id) REFERENCES nodes(id) ON DELETE RESTRICT;

ALTER TABLE ONLY route_policies
    ADD CONSTRAINT route_policies_fallback_exit_id_fkey FOREIGN KEY (fallback_exit_id) REFERENCES nodes(id) ON DELETE RESTRICT;

ALTER TABLE ONLY route_policies
    ADD CONSTRAINT route_policies_others_exit_id_fkey FOREIGN KEY (others_exit_id) REFERENCES nodes(id) ON DELETE RESTRICT;

ALTER TABLE ONLY route_policies
    ADD CONSTRAINT route_policies_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES namespaces(id) ON DELETE RESTRICT;

ALTER TABLE ONLY traffic_samples
    ADD CONSTRAINT traffic_samples_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE ONLY user_traffic
    ADD CONSTRAINT user_traffic_entry_node_id_fkey FOREIGN KEY (entry_node_id) REFERENCES nodes(id) ON DELETE CASCADE;

ALTER TABLE ONLY user_traffic
    ADD CONSTRAINT user_traffic_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE ONLY users
    ADD CONSTRAINT users_group_id_fkey FOREIGN KEY (group_id) REFERENCES groups(id) ON DELETE RESTRICT;

ALTER TABLE ONLY users
    ADD CONSTRAINT users_owner_id_fkey FOREIGN KEY (owner_id) REFERENCES namespaces(id) ON DELETE RESTRICT;
COMMIT;
