# MCP Skills

MCP Skills lets an Agently workspace use reusable skills published by configured
MCP servers. A skill is a folder centered on a portable `SKILL.md` file. It can
describe a workflow, refer to supporting files, and request tools that are
relevant to that workflow.

The feature gives agents one skill catalog across local workspace skills and
skills supplied by MCP servers. A remote skill remains associated with the MCP
server and user that supplied it; it never becomes a trusted local skill.

## What users see

Agents use the same global skill controls for local and remote skills:

```text
llm/skills:list
llm/skills:get
llm/skills:activate
```

`list` returns the skills visible to the current agent. Local skills and
eligible MCP skills appear in one result. Remote entries include a qualified
reference so that skills with the same name remain distinct.

`get` reads a skill without changing the conversation. It is useful when the
agent needs to inspect instructions or a supporting file before deciding
whether to use the skill.

`activate` loads a selected skill into the active task. It follows the
skill's configured inline, fork, or detach behavior where that behavior is
permitted by Agently.

MCP server names stay in skill references rather than becoming separate agent
tools. For example, the catalog may show:

```text
review                  local workspace skill
github/review           MCP skill from the configured github server
gitlab/review           MCP skill from the configured gitlab server
```

Use a qualified reference whenever a name is ambiguous.

## Discovering skills

MCP discovery happens only when the agent asks to list, get, or activate a
remote skill. Starting Agently, loading a normal prompt, and watching local
skill directories do not fetch remote skill catalogs.

Agently supports the standard
[`io.modelcontextprotocol/skills`](https://github.com/modelcontextprotocol/ext-skills/blob/0e85d4db8860a305c857f26fdede64f416675b92/specification/stable/skills.mdx)
extension. A compatible server publishes skill metadata with `skills/list`
and returns one skill entry with `skills/get`. Agently reads the actual
`SKILL.md` and supporting files through standard `resources/read` calls.

A server may use `skill://` URIs or another URI scheme that is natural for its
domain. Agently treats the configured server identity and the source URI
together as the remote skill identity.

## Configuring an MCP skill source

Enable discovery on the MCP server definition:

```yaml
name: github
transport:
  type: streamable
  url: https://example.com/mcp

skillDiscovery:
  enabled: true
  catalogId: github
  maxPages: 20
  maxSkills: 500
  timeoutSec: 10
```

`catalogId` is the short server label shown in qualified references. It
defaults to the configured server name. Use a unique, URI-safe value when the
server name is unsuitable for display.

Skill discovery is disabled unless `enabled: true` is set. Limits control how
much metadata Agently accepts during one listing request. They protect the
workspace from unexpectedly large remote catalogs without changing the server's
catalog itself.

An agent controls which skills it can see using its existing `skills:`
patterns. A pattern can match the portable skill name, `server/name`, or the
qualified skill URI. Negative patterns remove matching skills. An agent with no
skill selection does not receive MCP skills.

## Selecting and reading a skill

An MCP skill is addressed by the configured server and the source skill URI.
The result returned by `list` carries a durable qualified reference. Agently
also accepts an explicit server plus source URI:

```json
{
  "server": "github",
  "uri": "docs://team/review/SKILL.md"
}
```

This lets a user or server point to a skill even when the server intentionally
omits it from a partial `skills/list` response.

When Agently loads a skill, it checks that the returned skill entry and its
`SKILL.md` agree. For a static manifest, every file read by the skill is
checked against its declared size and SHA-256 digest. If a server changes the
manifest, Agently requires the updated skill to be selected again rather than
silently replacing the active content.

Supporting files are read only from the skill's originating server and only if
they are included in that skill's manifest. A nested `SKILL.md` read as a
supporting file is ordinary content; it does not activate another skill.

## Working safely with remote skills

Remote skill text is server-provided context. Agently labels it with its source
when it enters the task and does not treat it as workspace-authored
instructions.

Remote skills cannot silently widen the agent's permissions:

- A remote `allowed-tools` declaration is shown as a requested tool set. It
  does not grant those tools automatically.
- Local command execution and remote preprocessing are unavailable to MCP
  skills.
- A remote skill cannot use another MCP server's resources or tools through its
  own instructions.
- Existing workspace policy and the current user's MCP authorization continue
  to apply to every call.

These boundaries apply across inline use, child-agent execution, and later
turns that restore a previously activated skill.

## Compatibility with older servers

Some existing MCP servers provide skill metadata through ordinary tools rather
than the standard extension. Agently can bridge those servers when the tool
names are configured explicitly:

```yaml
skillDiscovery:
  enabled: true
  tools:
    list: skills_list
    get: skills_get
```

The bridge calls these tools with `cursor` or `uri` arguments and expects
the same skill-entry shape used by the standard extension. It still reads files
through `resources/read` and applies the same identity and manifest checks.

Native MCP Skills support takes precedence whenever the server advertises the
standard extension.

## Publishing local skills through MCP

An Agently instance can publish selected local skills to other MCP clients.
Configure `skillItems` on the exposed MCP server. Each selected skill is
served as a standard MCP skill under an Agently-owned `skill://` URI, with a
complete immutable file manifest.

The exposed server publishes only selected local skills. It does not republish
skills federated from other MCP servers, avoiding recursive server graphs.

## Local development

The workspace currently uses local module replacements for `viant/mcp` and
`viant/mcp-protocol`, so MCP Skills behavior is exercised against the local
repositories while the related library changes are developed. Production
dependency releases can replace those local paths when published.
