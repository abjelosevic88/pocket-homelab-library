#!/usr/bin/env python3
"""Give the demo reader (Ana) a reading life: books finished over the last months,
two in progress, highlights, shelves, devices and six weeks of reading sessions.

    scripts/demo/seed.py http://127.0.0.1:18310 ~/.cache/pocket-library-demo/data/pocket-library.db

The sessions are backdated straight in the database, so stop the server for the
second half (the script asks before it writes there). Demo data only.
"""
import json
import random
import sqlite3
import sys
import time
import urllib.request
from datetime import datetime, timedelta, timezone

base, db = sys.argv[1].rstrip("/"), sys.argv[2]
random.seed(7)


def call(method, path, body=None, cookie=[None]):
    req = urllib.request.Request(base + "/api/v1" + path, method=method, data=None if body is None else json.dumps(body).encode())
    req.add_header("Content-Type", "application/json")
    if cookie[0]:
        req.add_header("Cookie", cookie[0])
    with urllib.request.urlopen(req) as res:
        if path == "/auth/login":
            cookie[0] = res.headers["Set-Cookie"].split(";")[0]
        raw = res.read()
        return json.loads(raw) if raw else None


call("POST", "/auth/login", {"email": "ana@example.com", "password": "demo-password"})
while call("GET", "/scan")["status"]["running"] or not call("GET", "/libraries")[0]["last_scan_at"]:
    time.sleep(2)
books = {b["title"]: b for b in call("GET", "/books?limit=200")["items"]}
print(len(books), "books")


def bid(title):
    for t, b in books.items():
        if t.lower().startswith(title.lower()):
            return b["id"]
    raise SystemExit("missing " + title)


today = datetime.now()
finished = [("Pride and Prejudice", 150, 10), ("Frankenstein", 120, 9), ("The Time Machine", 95, 8), ("Dracula", 70, 10),
            ("A Christmas Carol", 60, 7), ("The Hound of the Baskervilles", 45, 9), ("Jane Eyre", 33, 9),
            ("The Strange Case of Dr. Jekyll", 20, 8), ("Treasure Island", 12, 7), ("The War of the Worlds", 6, 8)]
for title, days, rating in finished:
    day = (today - timedelta(days=days)).strftime("%Y-%m-%d")
    call("PUT", f"/books/{bid(title)}/state", {"status": "read", "day": day, "rating": rating})
call("PUT", f"/books/{bid('Dracula')}/state", {"favorite": True})
call("PUT", f"/books/{bid('Frankenstein')}/state", {"favorite": True})
for title in ["Moby Dick", "Anna Karenina", "Crime and Punishment", "Great Expectations"]:
    call("PUT", f"/books/{bid(title)}/state", {"status": "want"})

reading = {"Wuthering Heights": 0.62, "The Picture of Dorian Gray": 0.27}
for title, pct in reading.items():
    b = call("GET", f"/books/{bid(title)}")
    call("POST", f"/books/{b['id']}/progress", {"file_id": b["files"][0]["id"], "percentage": pct, "locator": "", "seconds": 30})

notes = {
    "Wuthering Heights": [("Whatever our souls are made of, his and mine are the same.", "Catherine about Heathcliff", "yellow", 0.31),
                          ("I have dreamt in my life, dreams that have stayed with me ever after, and changed my ideas.", "", "blue", 0.29)],
    "Frankenstein": [("Beware; for I am fearless, and therefore powerful.", "", "pink", 0.71),
                     ("Nothing is so painful to the human mind as a great and sudden change.", "", "yellow", 0.55)],
    "Pride and Prejudice": [("It is a truth universally acknowledged, that a single man in possession of a good fortune, must be in want of a wife.", "The opening line", "green", 0.0)],
    "The Picture of Dorian Gray": [("The only way to get rid of a temptation is to yield to it.", "", "purple", 0.12)],
}
for title, items in notes.items():
    for text, note, color, pct in items:
        call("POST", f"/books/{bid(title)}/annotations", {"kind": "highlight", "text": text, "note": note, "color": color, "percentage": pct, "locator": ""})

victorian = call("POST", "/shelves", {"name": "Victorian gothic", "description": "Fog, letters and monsters", "icon": "moon"})
call("POST", f"/shelves/{victorian['id']}/books", {"book_ids": [bid(t) for t in ["Dracula", "Frankenstein", "The Strange Case of Dr. Jekyll", "The Picture of Dorian Gray", "Wuthering Heights"]]})
holmes = call("POST", "/shelves", {"name": "Sherlock Holmes, in order", "ordered": True, "icon": "list-ordered"})
call("POST", f"/shelves/{holmes['id']}/books", {"book_ids": [bid(t) for t in ["A Study in Scarlet", "The Adventures of Sherlock Holmes", "The Hound of the Baskervilles"]]})
call("POST", "/shelves", {"name": "Loved it", "kind": "smart", "filter": json.dumps({"rating_min": 9}), "icon": "heart"})

call("POST", "/devices", {"kind": "kindle", "name": "Kindle Paperwhite", "email": "ana_demo@kindle.com"})
call("POST", "/devices", {"kind": "crosspoint", "name": "Xteink X4", "settings": {"folder": "/Books", "push": False}})
call("POST", "/devices", {"kind": "koreader", "name": "Kobo Libra (KOReader)"})
call("PUT", "/settings", {"daily_goal": 30, "yearly_goal": 24})
print("state, highlights, shelves and devices seeded")

input("Stop the server now, then press Enter to backdate reading sessions… ")
con = sqlite3.connect(db)
uid = con.execute("SELECT id FROM users WHERE email = 'ana@example.com'").fetchone()[0]
devices = ["Web reader · Safari on Mac", "Xteink X4", "Kobo Libra (KOReader)", "Web reader · Safari on iPhone"]
ids = [bid(t) for t, _, _ in finished] + [bid(t) for t in reading]
for d in range(42, 0, -1):
    if random.random() < 0.18:
        continue
    day = today - timedelta(days=d)
    for _ in range(random.choice([1, 1, 2])):
        start = day.replace(hour=random.choice([7, 13, 21, 22]), minute=random.randint(0, 59), second=0, microsecond=0)
        secs = random.randint(12, 55) * 60
        pages = int(secs / 60 * random.uniform(0.6, 1.1))
        book = random.choice(ids)
        dev = random.choice(devices)
        src = "web" if dev.startswith("Web") else "kosync"
        s = start.astimezone(timezone.utc)
        con.execute("""INSERT INTO reading_sessions (user_id, book_id, device, source, day, started_at, ended_at, seconds, start_pct, end_pct, pages, estimated)
                       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)""",
                    (uid, book, dev, src, start.strftime("%Y-%m-%d"), s.isoformat().replace("+00:00", "Z"),
                     (s + timedelta(seconds=secs)).isoformat().replace("+00:00", "Z"), secs, 0.2, 0.3, pages, 1 if src == "kosync" else 0))
con.commit()
print("sessions backdated; start the server again")
