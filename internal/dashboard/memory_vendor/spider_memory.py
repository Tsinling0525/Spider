"""Spider's stdio host adapter for the unmodified octop-memory engine (MIT)."""
from __future__ import annotations

import json
import os
from pathlib import Path
import sqlite3
import sys
import uuid
from dataclasses import asdict

sys.path.insert(0, str(Path(__file__).parent))
from octop_memory import Memory
from octop_memory.adapters.bridge.handlers import Bridge
from octop_memory.ports.llm._protocol import LLMClientError


def emit(value):
    print(json.dumps(value, ensure_ascii=False), flush=True)


class HostLLM:
    def call_llm(self, prompt, *, system=None, **options):
        emit({"llm": {"prompt": prompt, "system": system, **options}})
        response = json.loads(sys.stdin.readline())
        if response.get("error"):
            raise LLMClientError(response["error"])
        return response["content"]


def execute(request):
    db_path = Path(request["db_path"])
    db_path.parent.mkdir(parents=True, exist_ok=True)
    memory = Memory("agent_spider", backend_config={"db_path": str(db_path)})
    os.chmod(db_path, 0o600)
    llm = HostLLM() if request.get("llm_enabled") else None
    bridge = Bridge(memory, llm=llm)
    method, params = request["method"], request.get("params") or {}
    try:
        if method == "capture_history":
            accepted = 0
            for session in params["sessions"]:
                session["events"] = [e for e in session.get("events", []) if not memory.get_raw(e["id"])]
                response = bridge.handle({"id": 1, "method": "capture", "params": session})
                if response.get("error"):
                    return response
                accepted += response["result"]["accepted"]
            return {"result": {"accepted": accepted}}
        if method == "capture":
            # Stable event ids make crash recovery and history backfill idempotent.
            params["events"] = [e for e in params.get("events", []) if not memory.get_raw(e["id"])]
        if method == "extract":
            session = params["session_id"]
            key = "spider_extracted:" + session
            bridge._runtime._extracted_ids[session] = set(json.loads(memory.get_meta(key) or "[]"))
            response = bridge.handle({"id": 1, "method": method, "params": params})
            if not response.get("error") and not response["result"].get("failure_reason"):
                memory.set_meta(key, json.dumps(sorted(bridge._runtime._extracted_ids[session])))
            return response
        if method == "tree":
            return {"result": {"items": [asdict(n) for n in memory.get_tree()]}}
        if method == "sessions":
            rows = memory.backend._conn.execute(
                f"SELECT DISTINCT session_id FROM {memory.namespace}_raw_events WHERE session_id IS NOT NULL ORDER BY session_id"
            ).fetchall()
            return {"result": {"items": [row[0] for row in rows]}}
        if method == "regenerate_pages":
            if llm is None:
                raise ValueError("请先配置记忆整理模型或当前聊天模型")
            from octop_memory.pipeline.page.regenerator import regenerate_dirty
            return {"result": asdict(regenerate_dirty(memory, llm=llm))}
        if method == "consolidate":
            from octop_memory.pipeline.lifecycle.consolidate import run_consolidation
            from octop_memory.pipeline.promotion.llm_hook import ModelEscalationHook
            return {"result": asdict(run_consolidation(memory, llm_hook=ModelEscalationHook(llm) if llm else None, dry_run=params.get("dry_run", True)))}
        if method == "storage_check":
            from octop_memory.pipeline.lifecycle.vacuum import check_storage
            return {"result": asdict(check_storage(memory))}
        if method == "slim":
            # Spider keeps chat history in state.json, rather than LangGraph checkpoints.
            # Back up the whole memory store, then reclaim SQLite space without pruning.
            before = db_path.stat().st_size
            backup = db_path.with_name(db_path.name + ".before-slim." + uuid.uuid4().hex[:12] + ".bak")
            emit({"progress": "backup"})
            with sqlite3.connect(db_path) as conn, sqlite3.connect(backup) as copy:
                conn.backup(copy)
                if copy.execute("PRAGMA integrity_check").fetchone()[0] != "ok":
                    raise ValueError("备份完整性检查失败")
            os.chmod(backup, 0o600)
            emit({"progress": "vacuum"})
            from octop_memory.pipeline.lifecycle.vacuum import compact_vacuum
            result = asdict(compact_vacuum(memory))
            return {"result": {**result, "backup_path": str(backup), "bytes_before": before, "bytes_after": db_path.stat().st_size}}
        if method in {"pack", "adopt", "doctor"}:
            from octop_memory.operations.migration.portable import SourceInfo, pack, adopt, doctor
            if method == "pack":
                counts = memory.backend.count_stats()
                source = SourceInfo("agent", str(db_path), memory.namespace, raw_event_count=max(1, counts.get("raw_events", 0)), agent_name="Spider")
                # The package writer supports atoms-only exports; its discovery guard
                # otherwise rejects stores lacking raw events. Manifest counts remain exact.
                return {"result": pack(source, out=params["package_path"]).to_dict()}
            if method == "adopt":
                if not params.get("dry_run", True):
                    backup = db_path.with_name(db_path.name + ".before-import." + uuid.uuid4().hex[:12] + ".bak")
                    with sqlite3.connect(db_path) as conn, sqlite3.connect(backup) as copy:
                        conn.backup(copy)
                    os.chmod(backup, 0o600)
                result = adopt(params["package_path"], "agent", memory.namespace, target_db_path=db_path, on_conflict=params.get("on_conflict", "skip"), host_rewrite="keep", dry_run=params.get("dry_run", True))
                return {"result": result.to_dict()}
            return {"result": doctor("agent", memory.namespace, db_path=str(db_path), compare_with=params.get("package_path")).to_dict()}
        return bridge.handle({"jsonrpc": "2.0", "id": 1, "method": method, "params": params})
    finally:
        memory.backend.close()


if __name__ == "__main__":
    try:
        result = execute(json.loads(sys.stdin.readline()))
        # Upstream tree dataclasses contain datetime values.
        print(json.dumps(result, ensure_ascii=False, default=str), flush=True)
    except Exception as exc:
        emit({"error": {"code": -32602 if isinstance(exc, ValueError) else -32603, "message": str(exc)}})
