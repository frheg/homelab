"""Starter for the hackathon pod: checks every connection the pod has and asks the model. Replace it with the product."""

import asyncio
import os
from pathlib import Path

import httpx
import psycopg
from fastapi import FastAPI
from fastapi.responses import HTMLResponse
from pydantic import BaseModel

LLM_URL = os.environ.get("LLM_URL", "http://llm.llm/v1")
LLM_MODEL = os.environ.get("LLM_MODEL", "qwen3.6-35b")
EMBED_URL = os.environ.get("EMBED_URL", "http://llm-cpu.llm/v1")
QDRANT_URL = os.environ.get("QDRANT_URL", "http://qdrant.qdrant:6333")
SEARXNG_URL = os.environ.get("SEARXNG_URL", "http://searxng.searxng")
DATA_DIR = Path(os.environ.get("DATA_DIR", "/srv/hackathon/data"))

# the proxy passes the full path (/hackathon/...); with root_path FastAPI matches routes without it
app = FastAPI(root_path=os.environ.get("ROOT_PATH", ""))


async def get(url: str) -> str:
    async with httpx.AsyncClient(timeout=10) as c:
        r = await c.get(url)
        r.raise_for_status()
        return f"HTTP {r.status_code}"


async def postgres() -> str:
    async with await psycopg.AsyncConnection.connect(os.environ["DATABASE_URL"], connect_timeout=5) as db:
        row = await (await db.execute("select current_database(), version()")).fetchone()
        return f"{row[0]}: {row[1].split(' on ')[0]}"


async def data() -> str:
    files = sorted(p.name for p in DATA_DIR.iterdir()) if DATA_DIR.is_dir() else []
    return f"{len(files)} entries in {DATA_DIR}" + (f": {', '.join(files[:8])}" if files else "")


@app.get("/api/status")
async def status():
    checks = {
        "llm": get(f"{LLM_URL}/models"),
        "embeddings": get(f"{EMBED_URL}/models"),
        "postgres": postgres(),
        "qdrant": get(f"{QDRANT_URL}/collections"),
        "searxng": get(f"{SEARXNG_URL}/healthz"),
        "internet": get("https://pypi.org/simple/fastapi/"),
        "data": data(),
    }
    results = await asyncio.gather(*checks.values(), return_exceptions=True)
    return {
        k: {"ok": not isinstance(v, Exception), "detail": f"{type(v).__name__}: {v}" if isinstance(v, Exception) else v}
        for k, v in zip(checks, results)
    }


class Question(BaseModel):
    q: str


@app.post("/api/ask")
async def ask(body: Question):
    # the first question may wait for the model to load on the GPU (up to a minute or two)
    async with httpx.AsyncClient(timeout=300) as c:
        r = await c.post(
            f"{LLM_URL}/chat/completions",
            json={"model": LLM_MODEL, "messages": [{"role": "user", "content": body.q}]},
        )
        r.raise_for_status()
        return {"answer": r.json()["choices"][0]["message"]["content"]}


PAGE = """<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<title>Hackathon</title>
<style>body{font:15px system-ui;max-width:44em;margin:2em auto;padding:0 1em}td{padding:.2em .8em .2em 0;vertical-align:top}
.ok{color:#2a7a2a}.bad{color:#b02020}textarea{width:100%;height:5em}pre{white-space:pre-wrap;background:#f4f4f4;padding:1em}</style>
<h1>Hackathon starter</h1>
<table id="s"><tr><td>checking...</td></tr></table>
<h2>Ask the model</h2>
<textarea id="q">In two sentences: what is a wellbore?</textarea>
<button onclick="ask()">Ask</button>
<pre id="a"></pre>
<script>
// relative URLs, so the page works under /hackathon/
fetch('api/status').then(r => r.json()).then(s => {
  document.getElementById('s').innerHTML = Object.entries(s).map(([k, v]) =>
    `<tr><td>${k}</td><td class="${v.ok ? 'ok' : 'bad'}">${v.ok ? 'ok' : 'failed'}</td><td>${v.detail}</td></tr>`).join('')
})
async function ask() {
  const a = document.getElementById('a')
  a.textContent = 'asking (the first answer can take a minute while the model loads)...'
  const r = await fetch('api/ask', {method: 'POST', headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({q: document.getElementById('q').value})})
  a.textContent = r.ok ? (await r.json()).answer : `failed: HTTP ${r.status}`
}
</script>"""


@app.get("/", response_class=HTMLResponse)
async def index():
    return PAGE
