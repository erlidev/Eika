/** What the connection learned of an MCP server, and where its sign-in stands. */

import type { MCPServerDetails } from "@/api/types";
import { Notice } from "@/components/Notice";
import { OutputBlock } from "@/components/OutputBlock";
import { eraText, location, transportLabels } from "@/features/mcp/describe";

/** Fields is a dense list of what is known, leaving out what is not. */
function Fields({ fields }: { fields: [string, React.ReactNode][] }) {
  const shown = fields.filter(([, v]) => v !== undefined && v !== null && v !== "");
  if (shown.length === 0) return null;
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
      {shown.map(([name, value]) => (
        <div key={name} className="contents">
          <dt className="text-muted-foreground">{name}</dt>
          <dd className="min-w-0 break-words">{value}</dd>
        </div>
      ))}
    </dl>
  );
}

function Mono({ children }: { children: React.ReactNode }) {
  return <span className="font-mono">{children}</span>;
}

export function AboutTab({ details }: { details: MCPServerDetails }) {
  const c = details.connection;
  const auth = details.auth;
  const server = details.server;
  const capabilities = c
    ? [
        c.capabilities.tools && "tools",
        c.capabilities.tools_list_changed && "tool list updates",
        c.capabilities.resources && "resources",
        c.capabilities.resources_subscribe && "resource subscriptions",
        c.capabilities.resources_list_changed && "resource list updates",
        c.capabilities.prompts && "prompts",
        c.capabilities.prompts_list_changed && "prompt list updates",
        c.capabilities.logging && "logging",
        c.capabilities.completions && "completions",
        ...(c.capabilities.extensions ?? []),
        ...(c.capabilities.experimental ?? []).map((x) => `${x} (experimental)`),
      ].filter((x): x is string => typeof x === "string")
    : [];
  return (
    <div className="space-y-4">
      <section className="space-y-2">
        <h4 className="text-sm font-medium">Connection</h4>
        {c === undefined ? (
          <p className="text-muted-foreground text-xs">
            Not connected successfully yet, so nothing is known of the server.
          </p>
        ) : (
          <>
            <Fields
              fields={[
                ["Server", `${c.server_info.title ?? c.server_info.name} ${c.server_info.version}`],
                ["Description", c.server_info.description],
                [
                  "Website",
                  c.server_info.website_url === undefined ? undefined : (
                    <a
                      href={c.server_info.website_url}
                      target="_blank"
                      rel="noreferrer"
                      className="text-primary font-mono hover:underline"
                    >
                      {c.server_info.website_url}
                    </a>
                  ),
                ],
                ["Protocol", <Mono key="p">{eraText(c)}</Mono>],
                [
                  "Also speaks",
                  (c.supported_versions ?? []).filter((v) => v !== c.protocol_version).length >
                  0 ? (
                    <Mono key="v">
                      {(c.supported_versions ?? [])
                        .filter((v) => v !== c.protocol_version)
                        .join(", ")}
                    </Mono>
                  ) : undefined,
                ],
                ["Transport", transportLabels[c.transport]],
                ["Offers", capabilities.join(", ")],
                [
                  "Lists read",
                  details.fetched_at === undefined ? undefined : (
                    <Mono key="f">{details.fetched_at.replace("T", " ").slice(0, 19)} UTC</Mono>
                  ),
                ],
              ]}
            />
            {c.instructions !== undefined && c.instructions !== "" && (
              <div className="space-y-1">
                <p className="text-muted-foreground text-xs">
                  Instructions the server gives the model
                </p>
                <OutputBlock
                  label="server instructions"
                  maxHeightClass="max-h-40"
                  className="whitespace-pre-wrap"
                >
                  {c.instructions}
                </OutputBlock>
              </div>
            )}
          </>
        )}
      </section>
      {server.kind === "http" ? (
        <section className="space-y-2">
          <h4 className="text-sm font-medium">Authorization</h4>
          <Fields
            fields={[
              [
                "Signed in",
                auth.authorized
                  ? auth.has_refresh_token
                    ? "yes, and the token is refreshed before it expires"
                    : "yes, until the token expires"
                  : "no",
              ],
              [
                "Authorization server",
                auth.issuer === undefined ? undefined : <Mono key="i">{auth.issuer}</Mono>,
              ],
              [
                "Resource",
                auth.resource === undefined ? undefined : <Mono key="r">{auth.resource}</Mono>,
              ],
              ["Scope", auth.scope === undefined ? undefined : <Mono key="s">{auth.scope}</Mono>],
              [
                "Token expires",
                auth.expires_at === undefined ? undefined : (
                  <Mono key="e">{auth.expires_at.replace("T", " ").slice(0, 19)} UTC</Mono>
                ),
              ],
              [
                "Client",
                auth.client_id === undefined ? undefined : <Mono key="c">{auth.client_id}</Mono>,
              ],
              [
                "Registered",
                auth.registration === undefined
                  ? undefined
                  : {
                      preregistered: "by hand",
                      metadata_document: "by Eika's client metadata document",
                      dynamic: "dynamically, by Eika",
                    }[auth.registration],
              ],
              [
                "Asked for",
                auth.challenge === undefined
                  ? undefined
                  : [
                      auth.challenge.scope === undefined ? "" : `scope ${auth.challenge.scope}`,
                      auth.challenge.error_description ?? auth.challenge.error ?? "",
                    ]
                      .filter((x) => x !== "")
                      .join("; ") || "authorization",
              ],
            ]}
          />
        </section>
      ) : (
        <Notice>
          This server runs as <span className="font-mono">{location(server)}</span> in each
          workspace that uses it.
          {(server.env_names ?? []).length > 0 &&
            ` Its variables (${(server.env_names ?? []).join(", ")}) are visible to the agent there.`}
        </Notice>
      )}
    </div>
  );
}
