"""Verify additive AG-UI upgrades in disposable MySQL schemas.

Usage: python3 tools/schema/test_mysql_protocol_reuse.py CONTAINER
The container must provide mysql and MYSQL_ROOT_PASSWORD. Credentials stay inside
its process environment. No existing schemas are modified.
"""
import pathlib
import subprocess
import sys
import uuid

ROOT = pathlib.Path(__file__).resolve().parents[2]
CONTAINER = sys.argv[1]
MYSQL = ["docker", "exec", "-i", CONTAINER, "sh", "-c",
         'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" exec mysql --user=root --batch --skip-column-names']


def query(sql):
    result = subprocess.run(MYSQL, input=sql, text=True, capture_output=True)
    if result.returncode:
        raise RuntimeError(result.stderr)
    return result.stdout.strip()


def check_versioned(filename, original_name):
    name = "agui_schema_test_" + uuid.uuid4().hex
    full = (ROOT / "script/mysql" / filename).read_text()
    marker = "-- Version 41 was an unreleased AG-UI POC."
    assert marker in full, "Missing pre-protocol migration boundary"
    full = full.replace("`" + original_name + "`", "`" + name + "`")
    old = full.split(marker)[0] + "\nDELIMITER ;\n"
    try:
        query(old)
        query(f"""USE `{name}`;
INSERT INTO conversation(id,title,created_by_user_id) VALUES('keep','Original','owner');
INSERT INTO turn(id,conversation_id,status) VALUES('turn','keep','succeeded');
INSERT INTO run(id,conversation_id,turn_id,status,checkpoint_data) VALUES('execution','keep','turn','completed','checkpoint');
INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,inline_body) VALUES('payload','tool_response','application/json',2,'inline','{{}}');
CREATE TABLE agui_thread(id INT PRIMARY KEY);
CREATE TABLE agui_run(id INT PRIMARY KEY);
CREATE TABLE agui_event(id INT PRIMARY KEY);
CREATE TABLE agui_lease(id INT PRIMARY KEY);
""")
        query(full)
        query(full)
        assert query(f"SELECT version_number FROM `{name}`.schema_version;") == "42"
        assert query(f"SELECT title,protocol_only FROM `{name}`.conversation WHERE id='keep';") == "Original\t0"
        assert query(f"SELECT status,run_kind,checkpoint_data FROM `{name}`.run WHERE id='execution';") == "completed\texecution\tcheckpoint"
        assert query(f"SELECT kind,inline_body FROM `{name}`.call_payload WHERE id='payload';") == "tool_response\t{}"
        assert query(f"SELECT COUNT(*) FROM information_schema.TABLES WHERE TABLE_SCHEMA='{name}' AND TABLE_NAME IN ('agui_thread','agui_run','agui_event','agui_lease');") == "0"
        query(f"""USE `{name}`;
INSERT INTO run(id,conversation_id,run_kind,protocol_key,protocol_status,protocol_run_id)
VALUES('protocol','keep','agui',SHA2('opaque',256),'finished',_binary'OPAQUE ');
INSERT INTO call_payload(id,kind,mime_type,size_bytes,storage,inline_body,run_id,sequence)
VALUES('event','agui.event','application/json',2,'inline','{{}}','protocol',1);
""")
        assert query(f"SELECT HEX(protocol_run_id) FROM `{name}`.run WHERE id='protocol';") == "4F504151554520"
        duplicate = f"INSERT INTO `{name}`.call_payload(id,kind,mime_type,size_bytes,storage,inline_body,run_id,sequence) VALUES('duplicate','agui.event','application/json',2,'inline','{{}}','protocol',1);"
        try:
            query(duplicate)
        except RuntimeError as error:
            assert "Duplicate entry" in str(error), str(error)
        else:
            raise AssertionError("Journal sequence uniqueness was not enforced")
        query(f"DELETE FROM `{name}`.run WHERE id='protocol';")
        assert query(f"SELECT COUNT(*) FROM `{name}`.call_payload WHERE id='event';") == "0"
        assert query(f"SELECT COUNT(*) FROM `{name}`.run WHERE id='execution';") == "1"
        print(filename + ": PASS (upgrade twice, preservation, opaque ID, uniqueness, cascade)")
    finally:
        query("DROP DATABASE IF EXISTS `" + name + "`;")


check_versioned("schema_versioned.ddl", "agently")
check_versioned("schema_versioned_steward.ddl", "agently_steward")
