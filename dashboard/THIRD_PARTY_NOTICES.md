# Third-party references

The dashboard's three-column chat layout, composer connector chips, custom MCP editor,
separate STT/TTS interaction, and provider/model configuration flow were adapted from [TencentCloud/Octop](https://github.com/TencentCloud/Octop).

- Reference commit: `4b17f1cfac6c8092d8554b94632af30ce7f51966`.
- `src/lib/connectors.ts` adapts the accent palette/hash and header parser from
  `dashboard/src/pages/Agent/Connectors/customMcpUtils.ts`.
- Reference UI files: `dashboard/src/layouts/Sidebar.module.less`,
  `dashboard/src/pages/Chat/chatInputCore.partial.less`, and the Chat / Connectors pages.
- Voice flow reference: `src/octop/api/routers/voice.py`.
- Model configuration references: `src/octop/api/routers/providers.py`,
  `dashboard/src/api/modules/provider.ts`, and `dashboard/src/api/modules/voice.ts`.
- The four provider logo files under `src/assets/providers/` were copied from
  Octop's `dashboard/src/assets/providers/` at the reference commit above.
  Provider names and logos identify their respective services.
- Five connector logos under `src/assets/connectors/`, catalog names,
  descriptions, colors, categories, credential hints and guide links were copied
  from Octop's connector catalog and dashboard assets. Brand marks identify the
  respective services; Spider is not affiliated with those providers.
- `../internal/dashboard/octop/qq_mail.py` and `mail_servers.py` are unmodified
  copies of Octop's standard-library mailbox adapter and mail host presets.
  `qq_mail_tools.json` and `baidu_map_tools.json` reproduce the original tool
  definitions. The directory includes a copy of Octop's MIT license.
  Spider's `mail_bridge.py` supplies the stdin interface and bounded output.
- Go built-in connector wire formats and Notion OAuth flow adapt
  `src/octop/infra/connectors/{catalog.py,builder.py,oauth/mcp.py}` and the
  `gateway/adapters/baidu_map.py` adapter. They preserve the selected services'
  credential and tool semantics while using Spider's storage and approvals.
- Memory management adapts Octop's `src/octop/infra/agents/memory/`,
  `src/octop/api/routers/{memory,memory_portable}.py` and dashboard Memory pages.
  `src/lib/memory-types.ts` preserves Octop's dashboard DTOs. Spider supplies the
  Svelte UI, Go HTTP / scheduling / admission and stdio model adapter.
- The complete `octop-memory` 1.0.0 Python package (105 upstream source files)
  is copied unchanged into `../internal/dashboard/memory_vendor/octop_memory/`.
  Source: https://github.com/TencentCloud/octop-memory (MIT, orcakit).
  Original file hashes are recorded in `memory_vendor/UPSTREAM.json`; its MIT
  notice is preserved in `memory_vendor/LICENSE-octop-memory`.
  The same package's distribution metadata is included alongside the source.
  `spider_memory.py` is Spider's adapter. SQLite business memory is used;
  Spider's Go chat storage does not require the optional LangGraph / PostgreSQL /
  vector / standalone CLI dependencies included by the upstream package.
- Octop uses React / Ant Design / Python. Spider implements these capabilities
  in Svelte / TypeScript / Go and reuses the mailbox Python adapter; it does not
  vendor Octop's entire dashboard or backend.
- Octop's MIT copyright and permission notice is included in [LICENSE-Octop](LICENSE-Octop).

The Svelte 5 entry point, `shared` components, `lib` ports and transport adapters,
Vite development proxy, and centralized styling tokens follow the adjacent `mantle-app`
workspace. The Spider brand icon is an original vector created for this dashboard.
