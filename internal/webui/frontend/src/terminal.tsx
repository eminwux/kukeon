import { useEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import "@xterm/xterm/css/xterm.css";

type Container = { id: string; attachable?: boolean; root?: boolean };

export function AttachTerminal({ path, containers }: { path: string; containers: Container[] }) {
  const eligible = containers.filter(container => container.attachable && !container.root);
  const [selected, setSelected] = useState(eligible[0]?.id ?? "");
  const [active, setActive] = useState(false);
  const [message, setMessage] = useState("");
  const mount = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!active || !mount.current) return;
    const term = new Terminal({ cursorBlink: true, convertEol: false, fontSize: 14 });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(mount.current);
    fit.fit();
    const socket = new WebSocket(`${location.protocol === "https:" ? "wss:" : "ws:"}//${location.host}${path}/containers/${encodeURIComponent(selected)}/attach`);
    socket.binaryType = "arraybuffer";
    let error = false;
    const resize = () => {
      fit.fit();
      if (socket.readyState === WebSocket.OPEN) socket.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
    };
    socket.onopen = () => { setMessage("Connected"); resize(); term.focus(); };
    socket.onmessage = event => {
      if (typeof event.data !== "string") term.write(new Uint8Array(event.data));
      else {
        try { const result = JSON.parse(event.data); if (result.type === "error") { error = true; setMessage(result.message); } }
        catch { error = true; setMessage("Invalid terminal response."); }
      }
    };
    socket.onerror = () => { error = true; setMessage("Terminal connection failed."); };
    socket.onclose = () => { if (!error) setMessage("Terminal connection closed."); };
    const input = term.onData(data => { if (socket.readyState === WebSocket.OPEN) socket.send(new TextEncoder().encode(data)); });
    const observer = new ResizeObserver(resize);
    observer.observe(mount.current);
    return () => { observer.disconnect(); input.dispose(); socket.close(); term.dispose(); };
  }, [active, selected, path]);

  return <section className="panel"><div className="panel-heading"><h2>Terminal</h2></div><div className="panel-body">
    {!eligible.length ? <p>No attachable containers are available.</p> : <><label>Container <select value={selected} disabled={active} onChange={event => setSelected(event.target.value)}>{eligible.map(container => <option key={container.id} value={container.id}>{container.id}</option>)}</select></label>{" "}<button onClick={() => { setMessage(""); setActive(!active); }}>{active ? "Detach" : "Attach"}</button></>}
    {message && <p role="status">{message}</p>}
    {active && <div ref={mount} className="terminal-screen" aria-label="Container terminal" />}
  </div></section>;
}
