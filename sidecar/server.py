"""spaCy NER sidecar for Bud.

Loads a spaCy model and exposes a simple HTTP API for fast entity extraction.
Used as a pre-filter: if spaCy finds entities, the main pipeline runs the
more expensive Ollama LLM extraction for relationships.

Usage:
    python server.py [--port 8099] [--model en_core_web_sm]
"""

import argparse
import logging
import time

from fastapi import FastAPI
from pydantic import BaseModel

logging.basicConfig(level=logging.INFO, format="%(asctime)s [ner-sidecar] %(message)s")
log = logging.getLogger(__name__)

app = FastAPI()

_nlp = None
_model_name = None


class ExtractRequest(BaseModel):
    text: str


class Entity(BaseModel):
    text: str
    label: str
    start: int
    end: int


class ExtractResponse(BaseModel):
    entities: list[Entity]
    has_entities: bool
    duration_ms: float


@app.post("/extract")
def extract(req: ExtractRequest) -> ExtractResponse:
    start = time.monotonic()
    doc = _nlp(req.text)
    entities = [
        Entity(text=ent.text, label=ent.label_, start=ent.start_char, end=ent.end_char)
        for ent in doc.ents
    ]
    duration = (time.monotonic() - start) * 1000
    if entities:
        log.info(
            "Found %d entities in %.0fms: %s",
            len(entities),
            duration,
            [(e.text, e.label) for e in entities],
        )
    return ExtractResponse(
        entities=entities,
        has_entities=len(entities) > 0,
        duration_ms=round(duration, 1),
    )


@app.get("/health")
def health():
    return {"status": "ok", "model": _model_name}


def main():
    global _nlp, _model_name

    parser = argparse.ArgumentParser(description="spaCy NER sidecar for Bud")
    parser.add_argument("--port", type=int, default=8099)
    parser.add_argument("--model", default="en_core_web_sm")
    args = parser.parse_args()

    import spacy

    _model_name = args.model
    log.info("Loading spaCy model %s...", _model_name)
    _nlp = spacy.load(_model_name)
    log.info("Model loaded. Starting server on port %d", args.port)

    import uvicorn

    uvicorn.run(app, host="127.0.0.1", port=args.port, log_level="warning")


if __name__ == "__main__":
    main()
