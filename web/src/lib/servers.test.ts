import type { Alert, Server } from "@/api/types";
import { alertMentions, hostAliases, probeInScope, projectScope, serverAliases, urlHost } from "./monitoring";

const alert = (labels: Record<string, string>): Alert => ({ name: "x", state: "firing", severity: "", summary: "", labels, since: null });

describe("hostAliases", () => {
  it("drops scheme, user, port and path and adds the short name", () => {
    expect(hostAliases("web-1.example.com:9100")).toEqual(["web-1.example.com", "web-1"]);
    expect(hostAliases("https://Web-1.Example.com/x?y")).toEqual(["web-1.example.com", "web-1"]);
    expect(hostAliases("root@web-1")).toEqual(["web-1"]);
    expect(hostAliases("192.0.2.10:22")).toEqual(["192.0.2.10"]);
    expect(hostAliases("[2001:db8::1]:22")).toEqual(["2001:db8::1"]);
    expect(hostAliases("2001:db8::1")).toEqual(["2001:db8::1"]);
    expect(hostAliases("  ")).toEqual([]);
  });
});

describe("scoping", () => {
  const web = {
    id: 1,
    name: "web-1",
    public_address: "203.0.113.10",
    tailscale_ip: "192.0.2.10",
    ssh_target: "root@web-1.example.com",
    projects: [{ key: "SHOP", name: "Shop", color: "", role: "" }],
  } as Server;
  const db = { ...web, id: 2, name: "db-1", public_address: "", tailscale_ip: "", ssh_target: "", projects: [] } as Server;

  it("matches alerts by any host-ish label, by name, address or alias", () => {
    const aliases = serverAliases(web);
    expect(alertMentions(alert({ instance: "web-1:9100" }), aliases)).toBe(true);
    expect(alertMentions(alert({ host: "192.0.2.10" }), aliases)).toBe(true);
    expect(alertMentions(alert({ nodename: "web-1.example.com" }), aliases)).toBe(true);
    expect(alertMentions(alert({ instance: "web-10:9100" }), aliases)).toBe(false);
    expect(alertMentions(alert({ summary: "web-1" }), aliases)).toBe(false);
  });

  it("builds a project's scope from its servers and endpoints", () => {
    const scope = projectScope({ key: "SHOP", name: "Shop" }, [web, db], [
      { url: "https://shop.example.com/health", project_key: "SHOP" },
      { url: "https://blog.example.com", project_key: "BLOG" },
    ]);
    expect([...scope.serverIds]).toEqual([1]);
    expect(scope.aliases.has("db-1")).toBe(false);
    expect(probeInScope({ url: "https://shop.example.com/", up: true, ssl_days_left: null }, scope)).toBe(true);
    expect(probeInScope({ url: "https://blog.example.com/", up: true, ssl_days_left: null }, scope)).toBe(false);
    expect(urlHost("not a url")).toBe("");
  });
});
