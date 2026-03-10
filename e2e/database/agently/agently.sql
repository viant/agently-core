-- MySQL dump 10.13  Distrib 8.0.41, for Linux (x86_64)
--
-- Host: localhost    Database: agently
-- ------------------------------------------------------
-- Server version	8.0.41

/*!40101 SET @OLD_CHARACTER_SET_CLIENT=@@CHARACTER_SET_CLIENT */;
/*!40101 SET @OLD_CHARACTER_SET_RESULTS=@@CHARACTER_SET_RESULTS */;
/*!40101 SET @OLD_COLLATION_CONNECTION=@@COLLATION_CONNECTION */;
/*!50503 SET NAMES utf8mb4 */;
/*!40103 SET @OLD_TIME_ZONE=@@TIME_ZONE */;
/*!40103 SET TIME_ZONE='+00:00' */;
/*!40014 SET @OLD_UNIQUE_CHECKS=@@UNIQUE_CHECKS, UNIQUE_CHECKS=0 */;
/*!40014 SET @OLD_FOREIGN_KEY_CHECKS=@@FOREIGN_KEY_CHECKS, FOREIGN_KEY_CHECKS=0 */;
/*!40101 SET @OLD_SQL_MODE=@@SQL_MODE, SQL_MODE='NO_AUTO_VALUE_ON_ZERO' */;
/*!40111 SET @OLD_SQL_NOTES=@@SQL_NOTES, SQL_NOTES=0 */;

--
-- Table structure for table `call_payload`
--

DROP TABLE IF EXISTS `call_payload`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `call_payload` (
  `id` varchar(255) NOT NULL,
  `tenant_id` varchar(255) DEFAULT NULL,
  `kind` varchar(255) NOT NULL,
  `subtype` text,
  `mime_type` text NOT NULL,
  `size_bytes` bigint NOT NULL,
  `digest` varchar(255) DEFAULT NULL,
  `storage` varchar(255) NOT NULL,
  `inline_body` longblob,
  `uri` text,
  `compression` varchar(255) NOT NULL DEFAULT 'none',
  `encryption_kms_key_id` text,
  `redaction_policy_version` text,
  `redacted` bigint NOT NULL DEFAULT '0',
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `schema_ref` text,
  PRIMARY KEY (`id`),
  KEY `idx_payload_tenant_kind` (`tenant_id`,`kind`,`created_at`),
  KEY `idx_payload_digest` (`digest`),
  CONSTRAINT `call_payload_chk_1` CHECK ((`kind` in (_utf8mb4'model_request',_utf8mb4'model_response',_utf8mb4'provider_request',_utf8mb4'provider_response',_utf8mb4'model_stream',_utf8mb4'tool_request',_utf8mb4'tool_response',_utf8mb4'elicitation_request',_utf8mb4'elicitation_response'))),
  CONSTRAINT `call_payload_chk_2` CHECK ((`storage` in (_utf8mb4'inline',_utf8mb4'object'))),
  CONSTRAINT `call_payload_chk_3` CHECK ((`compression` in (_utf8mb4'none',_utf8mb4'gzip',_utf8mb4'zstd'))),
  CONSTRAINT `call_payload_chk_4` CHECK ((`redacted` in (0,1))),
  CONSTRAINT `call_payload_chk_5` CHECK ((((`storage` = _utf8mb4'inline') and (`inline_body` is not null)) or ((`storage` = _utf8mb4'object') and (`inline_body` is null))))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping data for table `call_payload`
--

LOCK TABLES `call_payload` WRITE;
/*!40000 ALTER TABLE `call_payload` DISABLE KEYS */;
/*!40000 ALTER TABLE `call_payload` ENABLE KEYS */;
UNLOCK TABLES;

--
-- Table structure for table `conversation`
--

DROP TABLE IF EXISTS `conversation`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `conversation` (
  `id` varchar(255) NOT NULL,
  `summary` text,
  `last_activity` timestamp NULL DEFAULT NULL,
  `usage_input_tokens` bigint DEFAULT '0',
  `usage_output_tokens` bigint DEFAULT '0',
  `usage_embedding_tokens` bigint DEFAULT '0',
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NULL DEFAULT NULL,
  `created_by_user_id` varchar(255) DEFAULT NULL,
  `agent_id` varchar(255) DEFAULT NULL,
  `default_model_provider` text,
  `default_model` text,
  `default_model_params` text,
  `title` text,
  `conversation_parent_id` varchar(255) DEFAULT NULL,
  `conversation_parent_turn_id` varchar(255) DEFAULT NULL,
  `metadata` text,
  `visibility` varchar(255) NOT NULL DEFAULT 'private',
  `shareable` tinyint NOT NULL DEFAULT '0',
  `status` varchar(255) DEFAULT NULL,
  `scheduled` tinyint DEFAULT NULL,
  `schedule_id` varchar(255) DEFAULT NULL,
  `schedule_run_id` varchar(255) DEFAULT NULL,
  `schedule_kind` varchar(32) DEFAULT NULL,
  `schedule_timezone` varchar(64) DEFAULT NULL,
  `schedule_cron_expr` varchar(255) DEFAULT NULL,
  `external_task_ref` text,
  PRIMARY KEY (`id`),
  CONSTRAINT `conversation_chk_1` CHECK ((`shareable` in (0,1))),
  CONSTRAINT `conversation_chk_2` CHECK ((`scheduled` in (0,1)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping data for table `conversation`
--

LOCK TABLES `conversation` WRITE;
/*!40000 ALTER TABLE `conversation` DISABLE KEYS */;
/*!40000 ALTER TABLE `conversation` ENABLE KEYS */;
UNLOCK TABLES;

--
-- Table structure for table `generated_file`
--

DROP TABLE IF EXISTS `generated_file`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `generated_file` (
  `id` varchar(255) NOT NULL,
  `conversation_id` varchar(255) NOT NULL,
  `turn_id` varchar(255) DEFAULT NULL,
  `message_id` varchar(255) DEFAULT NULL,
  `provider` varchar(255) NOT NULL,
  `mode` varchar(255) NOT NULL,
  `copy_mode` varchar(255) NOT NULL,
  `status` varchar(255) NOT NULL DEFAULT 'ready',
  `payload_id` varchar(255) DEFAULT NULL,
  `container_id` varchar(255) DEFAULT NULL,
  `provider_file_id` varchar(255) DEFAULT NULL,
  `filename` varchar(255) DEFAULT NULL,
  `mime_type` varchar(255) DEFAULT NULL,
  `size_bytes` bigint DEFAULT NULL,
  `checksum` varchar(255) DEFAULT NULL,
  `error_message` text,
  `dedup_key` varchar(255) DEFAULT NULL,
  `expires_at` timestamp NULL DEFAULT NULL,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_gf_conversation` (`conversation_id`),
  KEY `idx_gf_dedup` (`dedup_key`),
  CONSTRAINT `fk_gf_conversation` FOREIGN KEY (`conversation_id`) REFERENCES `conversation` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping data for table `generated_file`
--

LOCK TABLES `generated_file` WRITE;
/*!40000 ALTER TABLE `generated_file` DISABLE KEYS */;
/*!40000 ALTER TABLE `generated_file` ENABLE KEYS */;
UNLOCK TABLES;

--
-- Table structure for table `message`
--

DROP TABLE IF EXISTS `message`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `message` (
  `id` varchar(255) NOT NULL,
  `conversation_id` varchar(255) NOT NULL,
  `turn_id` varchar(255) DEFAULT NULL,
  `archived` tinyint DEFAULT NULL,
  `sequence` bigint DEFAULT NULL,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NULL DEFAULT NULL,
  `created_by_user_id` varchar(255) DEFAULT NULL,
  `status` varchar(255) DEFAULT NULL,
  `mode` varchar(255) DEFAULT NULL,
  `role` varchar(255) NOT NULL,
  `type` varchar(255) NOT NULL DEFAULT 'text',
  `content` mediumtext,
  `raw_content` mediumtext,
  `summary` text,
  `context_summary` text,
  `tags` text,
  `interim` bigint NOT NULL DEFAULT '0',
  `elicitation_id` varchar(255) DEFAULT NULL,
  `parent_message_id` varchar(255) DEFAULT NULL,
  `superseded_by` varchar(255) DEFAULT NULL,
  `linked_conversation_id` varchar(255) DEFAULT NULL,
  `attachment_payload_id` varchar(255) DEFAULT NULL,
  `elicitation_payload_id` varchar(255) DEFAULT NULL,
  `tool_name` text,
  `embedding_index` longblob,
  `preamble` text,
  `iteration` int DEFAULT NULL,
  `phase` varchar(16) DEFAULT 'final',
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_message_turn_seq` (`turn_id`,`sequence`),
  KEY `fk_message_attachment_payload` (`attachment_payload_id`),
  KEY `fk_message_elicitation_payload` (`elicitation_payload_id`),
  KEY `idx_msg_conv_created` (`conversation_id`,`created_at` DESC),
  KEY `idx_message_iteration` (`turn_id`,`iteration`,`created_at`),
  CONSTRAINT `fk_message_attachment_payload` FOREIGN KEY (`attachment_payload_id`) REFERENCES `call_payload` (`id`) ON DELETE SET NULL,
  CONSTRAINT `fk_message_conversation` FOREIGN KEY (`conversation_id`) REFERENCES `conversation` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_message_elicitation_payload` FOREIGN KEY (`elicitation_payload_id`) REFERENCES `call_payload` (`id`) ON DELETE SET NULL,
  CONSTRAINT `fk_message_turn` FOREIGN KEY (`turn_id`) REFERENCES `turn` (`id`) ON DELETE SET NULL,
  CONSTRAINT `message_chk_1` CHECK (((`status` is null) or (`status` in (_utf8mb4'',_utf8mb4'pending',_utf8mb4'accepted',_utf8mb4'rejected',_utf8mb4'cancel',_utf8mb4'open',_utf8mb4'summary',_utf8mb4'summarized',_utf8mb4'completed',_utf8mb4'error',_utf8mb4'running',_utf8mb4'failed',_utf8mb4'canceled')))),
  CONSTRAINT `message_chk_2` CHECK ((`role` in (_utf8mb4'system',_utf8mb4'user',_utf8mb4'assistant',_utf8mb4'tool',_utf8mb4'chain'))),
  CONSTRAINT `message_chk_3` CHECK ((`type` in (_utf8mb4'text',_utf8mb4'tool_op',_utf8mb4'control',_utf8mb4'task',_utf8mb4'elicitation_response'))),
  CONSTRAINT `message_chk_4` CHECK ((`interim` in (0,1)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping data for table `message`
--

LOCK TABLES `message` WRITE;
/*!40000 ALTER TABLE `message` DISABLE KEYS */;
/*!40000 ALTER TABLE `message` ENABLE KEYS */;
UNLOCK TABLES;

--
-- Table structure for table `model_call`
--

DROP TABLE IF EXISTS `model_call`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `model_call` (
  `message_id` varchar(255) NOT NULL,
  `turn_id` varchar(255) DEFAULT NULL,
  `provider` text NOT NULL,
  `model` varchar(255) NOT NULL,
  `model_kind` varchar(255) NOT NULL,
  `error_code` text,
  `error_message` text,
  `finish_reason` text,
  `prompt_tokens` bigint DEFAULT NULL,
  `prompt_cached_tokens` bigint DEFAULT NULL,
  `completion_tokens` bigint DEFAULT NULL,
  `total_tokens` bigint DEFAULT NULL,
  `prompt_audio_tokens` bigint DEFAULT NULL,
  `completion_reasoning_tokens` bigint DEFAULT NULL,
  `completion_audio_tokens` bigint DEFAULT NULL,
  `completion_accepted_prediction_tokens` bigint DEFAULT NULL,
  `completion_rejected_prediction_tokens` bigint DEFAULT NULL,
  `status` varchar(255) NOT NULL,
  `started_at` timestamp NULL DEFAULT NULL,
  `completed_at` timestamp NULL DEFAULT NULL,
  `latency_ms` bigint DEFAULT NULL,
  `cost` double DEFAULT NULL,
  `trace_id` text,
  `span_id` text,
  `request_payload_id` varchar(255) DEFAULT NULL,
  `response_payload_id` varchar(255) DEFAULT NULL,
  `provider_request_payload_id` varchar(255) DEFAULT NULL,
  `provider_response_payload_id` varchar(255) DEFAULT NULL,
  `stream_payload_id` varchar(255) DEFAULT NULL,
  `run_id` varchar(255) DEFAULT NULL,
  `iteration` int DEFAULT NULL,
  PRIMARY KEY (`message_id`),
  KEY `fk_model_call_turn` (`turn_id`),
  KEY `fk_model_call_req_payload` (`request_payload_id`),
  KEY `fk_model_call_res_payload` (`response_payload_id`),
  KEY `fk_model_call_provider_req_payload` (`provider_request_payload_id`),
  KEY `fk_model_call_provider_res_payload` (`provider_response_payload_id`),
  KEY `fk_model_call_stream_payload` (`stream_payload_id`),
  KEY `idx_model_call_model` (`model`),
  KEY `idx_model_call_started_at` (`started_at`),
  KEY `idx_model_call_run` (`run_id`,`iteration`),
  CONSTRAINT `fk_model_call_provider_req_payload` FOREIGN KEY (`provider_request_payload_id`) REFERENCES `call_payload` (`id`) ON DELETE SET NULL,
  CONSTRAINT `fk_model_call_provider_res_payload` FOREIGN KEY (`provider_response_payload_id`) REFERENCES `call_payload` (`id`) ON DELETE SET NULL,
  CONSTRAINT `fk_model_call_req_payload` FOREIGN KEY (`request_payload_id`) REFERENCES `call_payload` (`id`) ON DELETE SET NULL,
  CONSTRAINT `fk_model_call_res_payload` FOREIGN KEY (`response_payload_id`) REFERENCES `call_payload` (`id`) ON DELETE SET NULL,
  CONSTRAINT `fk_model_call_stream_payload` FOREIGN KEY (`stream_payload_id`) REFERENCES `call_payload` (`id`) ON DELETE SET NULL,
  CONSTRAINT `fk_model_call_turn` FOREIGN KEY (`turn_id`) REFERENCES `turn` (`id`) ON DELETE SET NULL,
  CONSTRAINT `fk_model_calls_message` FOREIGN KEY (`message_id`) REFERENCES `message` (`id`) ON DELETE CASCADE,
  CONSTRAINT `model_call_chk_1` CHECK ((`model_kind` in (_utf8mb4'chat',_utf8mb4'completion',_utf8mb4'vision',_utf8mb4'reranker',_utf8mb4'embedding',_utf8mb4'other'))),
  CONSTRAINT `model_call_chk_2` CHECK ((`status` in (_utf8mb4'thinking',_utf8mb4'streaming',_utf8mb4'running',_utf8mb4'completed',_utf8mb4'failed',_utf8mb4'canceled')))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping data for table `model_call`
--

LOCK TABLES `model_call` WRITE;
/*!40000 ALTER TABLE `model_call` DISABLE KEYS */;
/*!40000 ALTER TABLE `model_call` ENABLE KEYS */;
UNLOCK TABLES;

--
-- Table structure for table `run`
--

DROP TABLE IF EXISTS `run`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `run` (
  `id` varchar(255) NOT NULL,
  `turn_id` varchar(255) DEFAULT NULL,
  `schedule_id` varchar(255) DEFAULT NULL,
  `conversation_id` varchar(255) DEFAULT NULL,
  `conversation_kind` varchar(32) NOT NULL DEFAULT 'interactive',
  `attempt` int NOT NULL DEFAULT '1',
  `resumed_from_run_id` varchar(255) DEFAULT NULL,
  `status` varchar(32) NOT NULL DEFAULT 'pending',
  `error_code` varchar(255) DEFAULT NULL,
  `error_message` text,
  `iteration` int NOT NULL DEFAULT '0',
  `max_iterations` int DEFAULT NULL,
  `checkpoint_response_id` varchar(255) DEFAULT NULL,
  `checkpoint_message_id` varchar(255) DEFAULT NULL,
  `checkpoint_data` mediumtext,
  `agent_id` varchar(255) DEFAULT NULL,
  `model_provider` varchar(255) DEFAULT NULL,
  `model` varchar(255) DEFAULT NULL,
  `worker_id` varchar(255) DEFAULT NULL,
  `worker_pid` int DEFAULT NULL,
  `worker_host` varchar(255) DEFAULT NULL,
  `lease_owner` varchar(255) DEFAULT NULL,
  `lease_until` timestamp NULL DEFAULT NULL,
  `last_heartbeat_at` timestamp NULL DEFAULT NULL,
  `security_context` mediumtext,
  `effective_user_id` varchar(255) DEFAULT NULL,
  `auth_authority` varchar(255) DEFAULT NULL,
  `auth_audience` varchar(255) DEFAULT NULL,
  `user_cred_url` text,
  `heartbeat_interval_sec` int DEFAULT '5',
  `scheduled_for` timestamp NULL DEFAULT NULL,
  `precondition_ran_at` timestamp NULL DEFAULT NULL,
  `precondition_passed` tinyint DEFAULT NULL,
  `precondition_result` mediumtext,
  `usage_prompt_tokens` bigint DEFAULT '0',
  `usage_completion_tokens` bigint DEFAULT '0',
  `usage_total_tokens` bigint DEFAULT '0',
  `usage_cost` double DEFAULT '0',
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NULL DEFAULT NULL,
  `started_at` timestamp NULL DEFAULT NULL,
  `completed_at` timestamp NULL DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `ux_run_schedule_slot` (`schedule_id`,`scheduled_for`),
  KEY `idx_run_turn` (`turn_id`,`attempt`),
  KEY `idx_run_worker` (`worker_id`,`status`),
  KEY `idx_run_heartbeat` (`status`,`last_heartbeat_at`),
  KEY `idx_run_conversation` (`conversation_id`,`status`),
  KEY `idx_run_schedule_status` (`schedule_id`,`status`),
  KEY `idx_run_pid` (`worker_pid`,`worker_host`,`status`),
  CONSTRAINT `fk_run_conversation` FOREIGN KEY (`conversation_id`) REFERENCES `conversation` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_run_schedule` FOREIGN KEY (`schedule_id`) REFERENCES `schedule` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_run_turn` FOREIGN KEY (`turn_id`) REFERENCES `turn` (`id`) ON DELETE SET NULL,
  CONSTRAINT `run_chk_1` CHECK ((`status` in (_utf8mb4'pending',_utf8mb4'prechecking',_utf8mb4'skipped',_utf8mb4'queued',_utf8mb4'running',_utf8mb4'completed',_utf8mb4'succeeded',_utf8mb4'failed',_utf8mb4'interrupted',_utf8mb4'canceled'))),
  CONSTRAINT `run_chk_2` CHECK ((`precondition_passed` in (0,1)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping data for table `run`
--

LOCK TABLES `run` WRITE;
/*!40000 ALTER TABLE `run` DISABLE KEYS */;
/*!40000 ALTER TABLE `run` ENABLE KEYS */;
UNLOCK TABLES;

--
-- Table structure for table `schedule`
--

DROP TABLE IF EXISTS `schedule`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `schedule` (
  `id` varchar(255) NOT NULL,
  `name` varchar(255) NOT NULL,
  `description` text,
  `agent_ref` varchar(255) NOT NULL,
  `model_override` varchar(255) DEFAULT NULL,
  `enabled` tinyint NOT NULL DEFAULT '1',
  `start_at` timestamp NULL DEFAULT NULL,
  `end_at` timestamp NULL DEFAULT NULL,
  `schedule_type` varchar(32) NOT NULL DEFAULT 'cron',
  `cron_expr` varchar(255) DEFAULT NULL,
  `interval_seconds` bigint DEFAULT NULL,
  `timezone` varchar(64) NOT NULL DEFAULT 'UTC',
  `task_prompt_uri` text,
  `task_prompt` mediumtext,
  `next_run_at` timestamp NULL DEFAULT NULL,
  `last_run_at` timestamp NULL DEFAULT NULL,
  `last_status` varchar(32) DEFAULT NULL,
  `last_error` text,
  `lease_owner` varchar(255) DEFAULT NULL,
  `lease_until` timestamp NULL DEFAULT NULL,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NULL DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `name` (`name`),
  KEY `idx_schedule_enabled_next` (`enabled`,`next_run_at`),
  KEY `idx_schedule_enabled_next_lease` (`enabled`,`next_run_at`,`lease_until`),
  CONSTRAINT `schedule_chk_1` CHECK ((`enabled` in (0,1))),
  CONSTRAINT `schedule_chk_2` CHECK ((`schedule_type` in (_utf8mb4'adhoc',_utf8mb4'cron',_utf8mb4'interval')))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping data for table `schedule`
--

LOCK TABLES `schedule` WRITE;
/*!40000 ALTER TABLE `schedule` DISABLE KEYS */;
/*!40000 ALTER TABLE `schedule` ENABLE KEYS */;
UNLOCK TABLES;

--
-- Table structure for table `tool_call`
--

DROP TABLE IF EXISTS `tool_call`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `tool_call` (
  `message_id` varchar(255) NOT NULL,
  `turn_id` varchar(255) DEFAULT NULL,
  `op_id` text NOT NULL,
  `attempt` bigint NOT NULL DEFAULT '1',
  `tool_name` varchar(255) NOT NULL,
  `tool_kind` varchar(255) NOT NULL,
  `status` varchar(255) NOT NULL,
  `request_hash` text,
  `error_code` text,
  `error_message` text,
  `retriable` bigint DEFAULT NULL,
  `started_at` timestamp NULL DEFAULT NULL,
  `completed_at` timestamp NULL DEFAULT NULL,
  `latency_ms` bigint DEFAULT NULL,
  `cost` double DEFAULT NULL,
  `trace_id` text,
  `span_id` text,
  `request_payload_id` varchar(255) DEFAULT NULL,
  `response_payload_id` varchar(255) DEFAULT NULL,
  `run_id` varchar(255) DEFAULT NULL,
  `iteration` int DEFAULT NULL,
  PRIMARY KEY (`message_id`),
  UNIQUE KEY `idx_tool_op_attempt` (`turn_id`,`op_id`(191),`attempt`),
  KEY `fk_tool_call_req_payload` (`request_payload_id`),
  KEY `fk_tool_call_res_payload` (`response_payload_id`),
  KEY `idx_tool_call_status` (`status`),
  KEY `idx_tool_call_name` (`tool_name`),
  KEY `idx_tool_call_op` (`turn_id`,`op_id`(191)),
  KEY `idx_tool_call_run` (`run_id`,`iteration`),
  CONSTRAINT `fk_tool_call_message` FOREIGN KEY (`message_id`) REFERENCES `message` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_tool_call_req_payload` FOREIGN KEY (`request_payload_id`) REFERENCES `call_payload` (`id`) ON DELETE SET NULL,
  CONSTRAINT `fk_tool_call_res_payload` FOREIGN KEY (`response_payload_id`) REFERENCES `call_payload` (`id`) ON DELETE SET NULL,
  CONSTRAINT `fk_tool_call_turn` FOREIGN KEY (`turn_id`) REFERENCES `turn` (`id`) ON DELETE SET NULL,
  CONSTRAINT `tool_call_chk_1` CHECK ((`tool_kind` in (_utf8mb4'general',_utf8mb4'resource'))),
  CONSTRAINT `tool_call_chk_2` CHECK ((`status` in (_utf8mb4'queued',_utf8mb4'running',_utf8mb4'completed',_utf8mb4'failed',_utf8mb4'skipped',_utf8mb4'canceled'))),
  CONSTRAINT `tool_call_chk_3` CHECK ((`retriable` in (0,1)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping data for table `tool_call`
--

LOCK TABLES `tool_call` WRITE;
/*!40000 ALTER TABLE `tool_call` DISABLE KEYS */;
/*!40000 ALTER TABLE `tool_call` ENABLE KEYS */;
UNLOCK TABLES;

--
-- Table structure for table `tool_approval_queue`
--

DROP TABLE IF EXISTS `tool_approval_queue`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `tool_approval_queue` (
  `id` varchar(255) NOT NULL,
  `user_id` varchar(255) NOT NULL,
  `conversation_id` varchar(255) DEFAULT NULL,
  `turn_id` varchar(255) DEFAULT NULL,
  `message_id` varchar(255) DEFAULT NULL,
  `tool_name` varchar(255) NOT NULL,
  `title` text,
  `arguments` longblob NOT NULL,
  `metadata` longblob DEFAULT NULL,
  `status` varchar(32) NOT NULL DEFAULT 'pending',
  `decision` text,
  `approved_by_user_id` varchar(255) DEFAULT NULL,
  `approved_at` timestamp NULL DEFAULT NULL,
  `executed_at` timestamp NULL DEFAULT NULL,
  `error_message` text,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NULL DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_taq_user_status_created` (`user_id`,`status`,`created_at`),
  KEY `idx_taq_conversation_status` (`conversation_id`,`status`,`created_at`),
  KEY `idx_taq_turn` (`turn_id`,`created_at`),
  KEY `fk_taq_message` (`message_id`),
  CONSTRAINT `fk_taq_conversation` FOREIGN KEY (`conversation_id`) REFERENCES `conversation` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_taq_message` FOREIGN KEY (`message_id`) REFERENCES `message` (`id`) ON DELETE SET NULL,
  CONSTRAINT `fk_taq_turn` FOREIGN KEY (`turn_id`) REFERENCES `turn` (`id`) ON DELETE SET NULL,
  CONSTRAINT `tool_approval_queue_chk_1` CHECK ((`status` in (_utf8mb4'pending',_utf8mb4'approved',_utf8mb4'rejected',_utf8mb4'canceled',_utf8mb4'executed',_utf8mb4'failed')))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping data for table `tool_approval_queue`
--

LOCK TABLES `tool_approval_queue` WRITE;
/*!40000 ALTER TABLE `tool_approval_queue` DISABLE KEYS */;
/*!40000 ALTER TABLE `tool_approval_queue` ENABLE KEYS */;
UNLOCK TABLES;

--
-- Table structure for table `turn`
--

DROP TABLE IF EXISTS `turn`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `turn` (
  `id` varchar(255) NOT NULL,
  `conversation_id` varchar(255) NOT NULL,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `queue_seq` bigint DEFAULT NULL,
  `status` varchar(255) NOT NULL,
  `error_message` text,
  `started_by_message_id` varchar(255) DEFAULT NULL,
  `retry_of` varchar(255) DEFAULT NULL,
  `agent_id_used` varchar(255) DEFAULT NULL,
  `agent_config_used_id` varchar(255) DEFAULT NULL,
  `model_override_provider` text,
  `model_override` text,
  `model_params_override` text,
  `run_id` varchar(255) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_turn_conversation` (`conversation_id`),
  KEY `idx_turn_conv_status_created` (`conversation_id`,`status`,`created_at`),
  KEY `idx_turn_conv_queue_seq` (`conversation_id`,`queue_seq`),
  CONSTRAINT `fk_turn_conversation` FOREIGN KEY (`conversation_id`) REFERENCES `conversation` (`id`) ON DELETE CASCADE,
  CONSTRAINT `turn_chk_1` CHECK ((`status` in (_utf8mb4'queued',_utf8mb4'pending',_utf8mb4'running',_utf8mb4'waiting_for_user',_utf8mb4'succeeded',_utf8mb4'failed',_utf8mb4'canceled')))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping data for table `turn`
--

LOCK TABLES `turn` WRITE;
/*!40000 ALTER TABLE `turn` DISABLE KEYS */;
/*!40000 ALTER TABLE `turn` ENABLE KEYS */;
UNLOCK TABLES;

--
-- Table structure for table `turn_queue`
--

DROP TABLE IF EXISTS `turn_queue`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `turn_queue` (
  `id` varchar(255) NOT NULL,
  `conversation_id` varchar(255) NOT NULL,
  `turn_id` varchar(255) NOT NULL,
  `message_id` varchar(255) NOT NULL,
  `queue_seq` bigint NOT NULL,
  `status` varchar(32) NOT NULL DEFAULT 'queued',
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NULL DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `ux_turn_queue_turn_id` (`turn_id`),
  UNIQUE KEY `ux_turn_queue_message_id` (`message_id`),
  KEY `idx_turn_queue_conv_status_seq` (`conversation_id`,`status`,`queue_seq`,`created_at`),
  CONSTRAINT `fk_turn_queue_conversation` FOREIGN KEY (`conversation_id`) REFERENCES `conversation` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_turn_queue_turn` FOREIGN KEY (`turn_id`) REFERENCES `turn` (`id`) ON DELETE CASCADE,
  CONSTRAINT `fk_turn_queue_message` FOREIGN KEY (`message_id`) REFERENCES `message` (`id`) ON DELETE CASCADE,
  CONSTRAINT `turn_queue_chk_1` CHECK ((`status` in (_utf8mb4'queued',_utf8mb4'dispatched',_utf8mb4'canceled',_utf8mb4'completed',_utf8mb4'failed')))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping data for table `turn_queue`
--

LOCK TABLES `turn_queue` WRITE;
/*!40000 ALTER TABLE `turn_queue` DISABLE KEYS */;
/*!40000 ALTER TABLE `turn_queue` ENABLE KEYS */;
UNLOCK TABLES;

--
-- Table structure for table `user_oauth_token`
--

DROP TABLE IF EXISTS `user_oauth_token`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `user_oauth_token` (
  `user_id` varchar(255) NOT NULL,
  `provider` varchar(128) NOT NULL,
  `enc_token` text NOT NULL,
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NULL DEFAULT NULL,
  `version` bigint NOT NULL DEFAULT '0',
  `lease_owner` varchar(255) DEFAULT NULL,
  `lease_until` timestamp NULL DEFAULT NULL,
  `refresh_status` varchar(32) NOT NULL DEFAULT 'idle',
  PRIMARY KEY (`user_id`,`provider`),
  CONSTRAINT `fk_uot_user` FOREIGN KEY (`user_id`) REFERENCES `users` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping data for table `user_oauth_token`
--

LOCK TABLES `user_oauth_token` WRITE;
/*!40000 ALTER TABLE `user_oauth_token` DISABLE KEYS */;
/*!40000 ALTER TABLE `user_oauth_token` ENABLE KEYS */;
UNLOCK TABLES;

--
-- Table structure for table `users`
--

DROP TABLE IF EXISTS `users`;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `users` (
  `id` varchar(255) NOT NULL,
  `username` varchar(255) NOT NULL,
  `display_name` varchar(255) DEFAULT NULL,
  `email` varchar(255) DEFAULT NULL,
  `provider` varchar(255) NOT NULL DEFAULT 'local',
  `subject` varchar(255) DEFAULT NULL,
  `hash_ip` varchar(255) DEFAULT NULL,
  `timezone` varchar(64) NOT NULL DEFAULT 'UTC',
  `default_agent_ref` varchar(255) DEFAULT NULL,
  `default_model_ref` varchar(255) DEFAULT NULL,
  `default_embedder_ref` varchar(255) DEFAULT NULL,
  `settings` text,
  `disabled` bigint NOT NULL DEFAULT '0',
  `created_at` timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` timestamp NULL DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `username` (`username`),
  UNIQUE KEY `ux_users_provider_subject` (`provider`,`subject`),
  KEY `ix_users_hash_ip` (`hash_ip`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;

--
-- Dumping data for table `users`
--

LOCK TABLES `users` WRITE;
/*!40000 ALTER TABLE `users` DISABLE KEYS */;
/*!40000 ALTER TABLE `users` ENABLE KEYS */;
UNLOCK TABLES;
/*!40103 SET TIME_ZONE=@OLD_TIME_ZONE */;

/*!40101 SET SQL_MODE=@OLD_SQL_MODE */;
/*!40014 SET FOREIGN_KEY_CHECKS=@OLD_FOREIGN_KEY_CHECKS */;
/*!40014 SET UNIQUE_CHECKS=@OLD_UNIQUE_CHECKS */;
/*!40101 SET CHARACTER_SET_CLIENT=@OLD_CHARACTER_SET_CLIENT */;
/*!40101 SET CHARACTER_SET_RESULTS=@OLD_CHARACTER_SET_RESULTS */;
/*!40101 SET COLLATION_CONNECTION=@OLD_COLLATION_CONNECTION */;
/*!40111 SET SQL_NOTES=@OLD_SQL_NOTES */;

-- Dump completed on 2026-02-28  2:08:57
