import { useEffect, useState } from "react";
import { Boxes, RefreshCw, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { APIError, api, type ServiceEntity } from "@/lib/api";

export function ServicesView() {
  const [items, setItems] = useState<ServiceEntity[]>([]);
  const [provider, setProvider] = useState("");
  const [error, setError] = useState("");

  async function refresh() {
    const result = await api.services(provider);
    setItems(result.items);
  }

  useEffect(() => {
    refresh().catch((caught) => setError(caught instanceof APIError ? caught.message : "Could not load service collectors"));
  }, [provider]);

  return <section className="workspace-grid services-view"><div className="section-heading"><div><Badge variant="outline"><ShieldCheck size={13} />Extensible collectors</Badge><h2>Services</h2><p>Provider adapters share bounded lifecycle, health, expiry, and redaction rules. A failed adapter cannot block host monitoring.</p></div><Button variant="ghost" size="icon" aria-label="Refresh services" onClick={() => refresh().catch(() => setError("Could not refresh services"))}><RefreshCw size={16} /></Button></div>{error && <p className="form-error" role="alert">{error}</p>}<div className="filter-row"><label>Provider<select value={provider} onChange={(event) => setProvider(event.target.value)}><option value="">All providers</option><option value="docker">Docker</option><option value="proxmox">Proxmox</option><option value="fake">Fixture provider</option></select></label><Badge variant="outline">{items.length} active entities</Badge></div>{items.length ? <div className="service-grid">{items.map((item) => <article className="service-card" key={`${item.provider}-${item.id}`}><div className="service-card-icon"><Boxes size={20} /></div><div><h3>{item.name}</h3><p>{item.provider} · {item.kind}{item.clusterId ? ` · ${item.clusterId}` : ""}</p><small>{item.status} · observed {new Date(item.observedAt).toLocaleString()}</small></div><Badge variant="outline">{item.deviceId ? "Associated" : "Unassociated"}</Badge></article>)}</div> : <div className="empty"><Boxes size={30} /><h2>No service entities</h2><p>Configure a collector on an enrolled device to see provider entities here. Host metrics remain independent.</p></div>}</section>;
}
