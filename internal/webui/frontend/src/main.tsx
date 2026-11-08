import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./style.css";

function App() {
  return (
    <main>
      <header>
        <span className="mark" aria-hidden="true">K</span>
        <span>Kukeon</span>
      </header>
      <section aria-labelledby="welcome">
        <p className="eyebrow">Your local workspace</p>
        <h1 id="welcome">A home for your workloads.</h1>
        <p className="description">
          The Kukeon web interface is taking shape. Realm browsing will be
          available in a future update.
        </p>
        <p className="status"><span aria-hidden="true" /> Preview</p>
      </section>
    </main>
  );
}

const root = document.getElementById("root");
if (!root) throw new Error("Missing application root");
createRoot(root).render(<StrictMode><App /></StrictMode>);
