--
-- PostgreSQL database dump
--

\restrict xidAqiyaVGlOM0vFpysioY9JkiI8J49JrMVouCfvcLWh9nIHgXqvnWZe4RxAEDs

-- Dumped from database version 17.6 (Debian 17.6-2.pgdg13+1)
-- Dumped by pg_dump version 17.6 (Debian 17.6-2.pgdg13+1)

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: _sqlx_migrations; Type: TABLE; Schema: public; Owner: op_succinct_user
--

CREATE TABLE public._sqlx_migrations (
    version bigint NOT NULL,
    description text NOT NULL,
    installed_on timestamp with time zone DEFAULT now() NOT NULL,
    success boolean NOT NULL,
    checksum bytea NOT NULL,
    execution_time bigint NOT NULL
);


ALTER TABLE public._sqlx_migrations OWNER TO op_succinct_user;

--
-- Name: chain_locks; Type: TABLE; Schema: public; Owner: op_succinct_user
--

CREATE TABLE public.chain_locks (
    l1_chain_id bigint NOT NULL,
    l2_chain_id bigint NOT NULL,
    locked_at timestamp without time zone
);


ALTER TABLE public.chain_locks OWNER TO op_succinct_user;

--
-- Name: requests; Type: TABLE; Schema: public; Owner: op_succinct_user
--

CREATE TABLE public.requests (
    id bigint NOT NULL,
    status smallint NOT NULL,
    req_type smallint NOT NULL,
    mode smallint NOT NULL,
    start_block bigint NOT NULL,
    end_block bigint NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    proof_request_id bytea,
    checkpointed_l1_block_number bigint,
    checkpointed_l1_block_hash bytea,
    execution_statistics jsonb DEFAULT 'null'::jsonb NOT NULL,
    witnessgen_duration bigint,
    execution_duration bigint,
    proof_request_time timestamp without time zone,
    prove_duration bigint,
    range_vkey_commitment bytea NOT NULL,
    aggregation_vkey_hash bytea,
    rollup_config_hash bytea NOT NULL,
    relay_tx_hash bytea,
    proof bytea,
    total_nb_transactions bigint NOT NULL,
    total_eth_gas_used bigint NOT NULL,
    total_l1_fees numeric(38,0) NOT NULL,
    total_tx_fees numeric(38,0) NOT NULL,
    l1_chain_id bigint NOT NULL,
    l2_chain_id bigint NOT NULL,
    contract_address bytea,
    prover_address bytea,
    l1_head_block_number bigint
);


ALTER TABLE public.requests OWNER TO op_succinct_user;

--
-- Name: requests_id_seq; Type: SEQUENCE; Schema: public; Owner: op_succinct_user
--

CREATE SEQUENCE public.requests_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


ALTER SEQUENCE public.requests_id_seq OWNER TO op_succinct_user;

--
-- Name: requests_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: op_succinct_user
--

ALTER SEQUENCE public.requests_id_seq OWNED BY public.requests.id;


--
-- Name: requests id; Type: DEFAULT; Schema: public; Owner: op_succinct_user
--

ALTER TABLE ONLY public.requests ALTER COLUMN id SET DEFAULT nextval('public.requests_id_seq'::regclass);


--
-- Data for Name: _sqlx_migrations; Type: TABLE DATA; Schema: public; Owner: op_succinct_user
--

COPY public._sqlx_migrations (version, description, installed_on, success, checksum, execution_time) FROM stdin;
1	init	2026-06-03 03:09:44.625505+00	t	\\xd6c22d9a7bb3b2090397ad9693a0caa13e897a92cb0f1eb05be9dad93a695848590510c0ebe1aeb8eb308f671b32537a	2669575
2	add fields to requests	2026-06-03 03:09:44.629282+00	t	\\x6a216a28e01227f8582ea3d441994af79f734a1e3d23ce370e742ea51f89068ab78825e31512618f493b33fbe1b4bfda	3572853
3	chain locks	2026-06-03 03:09:44.633755+00	t	\\x2efbf52cf0ef8780a0bee3eda20a07ab50c4e563cc1f236ed46c15b833e50a64f480920b019885802ac9827252e507f0	1907503
\.


--
-- Data for Name: chain_locks; Type: TABLE DATA; Schema: public; Owner: op_succinct_user
--

COPY public.chain_locks (l1_chain_id, l2_chain_id, locked_at) FROM stdin;
271828	20201	2026-06-03 03:10:51.210419
\.


--
-- Data for Name: requests; Type: TABLE DATA; Schema: public; Owner: op_succinct_user
--

COPY public.requests (id, status, req_type, mode, start_block, end_block, created_at, updated_at, proof_request_id, checkpointed_l1_block_number, checkpointed_l1_block_hash, execution_statistics, witnessgen_duration, execution_duration, proof_request_time, prove_duration, range_vkey_commitment, aggregation_vkey_hash, rollup_config_hash, relay_tx_hash, proof, total_nb_transactions, total_eth_gas_used, total_l1_fees, total_tx_fees, l1_chain_id, l2_chain_id, contract_address, prover_address, l1_head_block_number) FROM stdin;
1	1	0	1	1	61	2026-06-03 03:10:51.20718	2026-06-03 03:10:51.210648	\N	\N	\N	null	\N	\N	\N	\N	\\x0e5158b64c46007c04e5972727a2a26832337fbe765162294b0ce1ed0db36f9d	\N	\\x24550c1e3e0d30c845fce6a757019f3b335d687401546b2d11bab6892cca00c4	\N	\N	66	3121212	11240	100999969655240	271828	20201	\N	\N	144
\.


--
-- Name: requests_id_seq; Type: SEQUENCE SET; Schema: public; Owner: op_succinct_user
--

SELECT pg_catalog.setval('public.requests_id_seq', 1, true);


--
-- Name: _sqlx_migrations _sqlx_migrations_pkey; Type: CONSTRAINT; Schema: public; Owner: op_succinct_user
--

ALTER TABLE ONLY public._sqlx_migrations
    ADD CONSTRAINT _sqlx_migrations_pkey PRIMARY KEY (version);


--
-- Name: chain_locks chain_locks_pkey; Type: CONSTRAINT; Schema: public; Owner: op_succinct_user
--

ALTER TABLE ONLY public.chain_locks
    ADD CONSTRAINT chain_locks_pkey PRIMARY KEY (l1_chain_id, l2_chain_id);


--
-- Name: requests requests_pkey; Type: CONSTRAINT; Schema: public; Owner: op_succinct_user
--

ALTER TABLE ONLY public.requests
    ADD CONSTRAINT requests_pkey PRIMARY KEY (id);


--
-- PostgreSQL database dump complete
--

\unrestrict xidAqiyaVGlOM0vFpysioY9JkiI8J49JrMVouCfvcLWh9nIHgXqvnWZe4RxAEDs

