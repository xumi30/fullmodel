#!/usr/bin/env python3
"""
Search GitHub files via GitHub API, GitHub web search, or an external search engine.

Usage:
  export GITHUB_TOKEN=ghp_...
  python tools/getgitfile/getgitfile.py "openai api key"
  python tools/getgitfile/getgitfile.py --mode engine "失控 凯文凯利"
  python tools/getgitfile/getgitfile.py --mode engine --outputdir /tmp/books "西游记"

The script can:
  * use GitHub code search API for small indexed files,
  * use GitHub web search for public blob pages,
  * use an external search API (best for public large files such as PDFs on GitHub).

It ranks candidates locally and works better for queries like:
  "失控 凯文凯利"
matching filenames such as:
  "[失控-全人类的最终命运和结局].凯文·凯利.pdf"

`api` mode requires GITHUB_TOKEN (classic PAT or fine-grained with Contents read + Search).
`engine` mode supports one of:
  * Baidu web search (no API key)
  * SERPAPI_KEY
  * BRAVE_SEARCH_API_KEY
Uses GitHub raw downloads or blob-page extraction (no git clone).
"""

from __future__ import annotations

import json
import os
import re
import sys
import urllib.error
import urllib.parse
import urllib.request
from difflib import SequenceMatcher
from html import unescape
from typing import Any

# Skip files larger than this (bytes). Large PDFs/ebooks are common, so keep this generous.
MAX_FILE_BYTES = 50 * 1024 * 1024
# Read at most this many bytes to sniff binary content.
SNIFF_BYTES = 8192
USER_AGENT = "getgitfile/1.0 (GitHub Code Search downloader)"
API_VERSION = "2022-11-28"
MAX_RESULTS = 10
MAX_QUERIES = 8
MAX_PAGES_PER_QUERY = 3
PER_PAGE = 100
WEB_PER_PAGE = 10
WEB_MAX_PAGES = 3
API_TARGET_CANDIDATES = 40
ENGINE_RESULTS = 10
BINARY_SAFE_EXTENSIONS = {
    ".pdf",
    ".epub",
    ".mobi",
    ".azw",
    ".azw3",
    ".djvu",
    ".doc",
    ".docx",
    ".ppt",
    ".pptx",
    ".xls",
    ".xlsx",
    ".zip",
    ".rar",
    ".7z",
    ".tar",
    ".gz",
    ".bz2",
}
DOCUMENT_EXTENSIONS = {
    ".pdf",
    ".epub",
    ".mobi",
    ".azw",
    ".azw3",
    ".djvu",
    ".txt",
    ".md",
    ".rtf",
    ".doc",
    ".docx",
    ".html",
    ".htm",
}
DEBUG_ENABLED = os.environ.get("GETGITFILE_DEBUG", "").strip().lower() in {
    "1",
    "true",
    "yes",
    "on",
}


def eprint(*args: object) -> None:
    print(*args, file=sys.stderr)


def dprint(*args: object) -> None:
    if DEBUG_ENABLED:
        eprint("[debug]", *args)


def is_rate_limit_error(exc: Exception) -> bool:
    msg = str(exc).lower()
    return "rate limit exceeded" in msg or "secondary rate limit" in msg


def parse_args(argv: list[str]) -> tuple[str, str, str]:
    mode = "auto"
    output_dir = ""
    keyword_parts: list[str] = []
    i = 1
    while i < len(argv):
        arg = argv[i]
        if arg.startswith("--mode="):
            mode = arg.split("=", 1)[1].strip().lower()
        elif arg.startswith("--outputdir="):
            output_dir = arg.split("=", 1)[1].strip()
        elif arg == "--mode":
            i += 1
            if i >= len(argv):
                raise ValueError("missing value for --mode")
            mode = argv[i].strip().lower()
        elif arg == "--outputdir":
            i += 1
            if i >= len(argv):
                raise ValueError("missing value for --outputdir")
            output_dir = argv[i].strip()
        else:
            keyword_parts.append(arg)
        i += 1

    if mode not in {"auto", "api", "web", "engine"}:
        raise ValueError("mode must be one of: auto, api, web, engine")

    keyword = " ".join(keyword_parts).strip()
    if not keyword:
        raise ValueError('Usage: python getgitfile.py [--mode auto|api|web|engine] [--outputdir DIR] "<search keyword>"')
    return mode, keyword, output_dir


def safe_dir_name(keyword: str) -> str:
    """Spaces -> underscores; strip characters unsafe for directory names."""
    s = keyword.strip().replace(" ", "_")
    for c in '<>:"/\\|?*\x00':
        s = s.replace(c, "_")
    s = re.sub(r"_+", "_", s).strip("_")
    return s or "output"


def safe_local_filename(path: str) -> str:
    base = os.path.basename(path) or "file"
    base = re.sub(r'[<>:"/\\|?*\x00]', "_", base)
    return base or "file"


def build_filename_search_query(keyword: str) -> str:
    """
    Build GitHub code search `q` that matches the file basename only (not body).

    Uses `filename:` for each whitespace-separated token; GitHub ANDs qualifiers,
    so each token should appear in the filename as a substring.
    """
    tokens = [t for t in re.split(r"\s+", keyword.strip()) if t]
    if not tokens:
        raise ValueError("keyword is empty")
    parts: list[str] = []
    for t in tokens:
        if re.search(r'[^\w\-.]', t):
            escaped = t.replace("\\", "\\\\").replace('"', '\\"')
            parts.append(f'filename:"{escaped}"')
        else:
            parts.append(f"filename:{t}")
    return " ".join(parts)


def quote_search_term(term: str) -> str:
    if re.search(r'[^\w\-.]', term):
        escaped = term.replace("\\", "\\\\").replace('"', '\\"')
        return f'"{escaped}"'
    return term


def normalize_for_match(text: str) -> str:
    text = urllib.parse.unquote(text).lower()
    return re.sub(r"[\W_]+", "", text, flags=re.UNICODE)


def is_cjk_text(text: str) -> bool:
    return bool(text) and all("\u4e00" <= ch <= "\u9fff" for ch in text)


def expand_token_variants(token: str) -> list[str]:
    variants = [token]
    normalized = normalize_for_match(token)
    if normalized and normalized != token:
        variants.append(normalized)

    # Foreign names transliterated into Chinese are often written with separators
    # in filenames, e.g. "凯文凯利" vs "凯文·凯利".
    if is_cjk_text(normalized) and len(normalized) in (4, 6, 8) and len(normalized) % 2 == 0:
        for i in range(0, len(normalized), 2):
            piece = normalized[i : i + 2]
            if piece:
                variants.append(piece)

    deduped: list[str] = []
    seen: set[str] = set()
    for item in variants:
        if item and item not in seen:
            deduped.append(item)
            seen.add(item)
    return deduped


def extract_query_terms(keyword: str) -> list[str]:
    raw_tokens = [t for t in re.split(r"\s+", keyword.strip()) if t]
    terms: list[str] = []
    seen: set[str] = set()
    for token in raw_tokens:
        for variant in expand_token_variants(token):
            if variant not in seen:
                terms.append(variant)
                seen.add(variant)
    return terms


def build_search_queries(keyword: str) -> list[str]:
    keyword = keyword.strip()
    if not keyword:
        raise ValueError("keyword is empty")

    raw_tokens = [t for t in re.split(r"\s+", keyword) if t]
    terms = extract_query_terms(keyword)
    queries: list[str] = []

    def add(query: str) -> None:
        query = query.strip()
        if query and query not in queries:
            queries.append(query)

    add(build_filename_search_query(keyword))
    add(f'filename:{quote_search_term(keyword)}')

    if len(raw_tokens) >= 2:
        longest = sorted(raw_tokens, key=len, reverse=True)
        add(f"filename:{quote_search_term(longest[0])} filename:{quote_search_term(longest[1])}")

    for term in sorted(terms, key=len, reverse=True):
        add(f"filename:{quote_search_term(term)}")

    for term in sorted(terms, key=len, reverse=True)[:3]:
        add(f"{quote_search_term(term)} in:path")

    return queries[:MAX_QUERIES]


def is_probably_binary(data: bytes) -> bool:
    if not data:
        return False
    if b"\x00" in data[:SNIFF_BYTES]:
        return True
    # Heuristic: high ratio of non-text control bytes (excluding common whitespace)
    sample = data[:SNIFF_BYTES]
    if len(sample) < 256:
        return False
    ctrl = sum(1 for b in sample if b < 32 and b not in (9, 10, 13))
    return ctrl / len(sample) > 0.3


def github_request(
    url: str,
    token: str,
    *,
    accept: str = "application/vnd.github+json",
    method: str = "GET",
    data: bytes | None = None,
) -> tuple[int, dict[str, str], bytes]:
    headers = {
        "Authorization": f"Bearer {token}",
        "Accept": accept,
        "User-Agent": USER_AGENT,
        "X-GitHub-Api-Version": API_VERSION,
    }
    req = urllib.request.Request(url, method=method, data=data, headers=headers)
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            status = resp.getcode() or 0
            hdrs = {k.lower(): v for k, v in resp.headers.items()}
            body = resp.read()
            return status, hdrs, body
    except urllib.error.HTTPError as e:
        err_body = e.read() if e.fp else b""
        return e.code, {k.lower(): v for k, v in e.headers.items()}, err_body


def search_code(token: str, query: str, page: int = 1, per_page: int = 100) -> dict[str, Any]:
    q = urllib.parse.urlencode({"q": query, "page": str(page), "per_page": str(per_page)})
    url = f"https://api.github.com/search/code?{q}"
    dprint("search_code request", {"page": page, "per_page": per_page, "query": query, "url": url})
    status, _hdrs, body = github_request(url, token)
    if status != 200:
        try:
            err = json.loads(body.decode("utf-8", errors="replace"))
            msg = err.get("message", body.decode("utf-8", errors="replace")[:500])
        except json.JSONDecodeError:
            msg = body.decode("utf-8", errors="replace")[:500]
        dprint("search_code error", {"status": status, "query": query, "message": msg})
        raise RuntimeError(f"GitHub search failed (HTTP {status}): {msg}")
    payload = json.loads(body.decode("utf-8"))
    dprint(
        "search_code response",
        {
            "page": page,
            "query": query,
            "total_count": int(payload.get("total_count") or 0),
            "items": len(payload.get("items") or []),
        },
    )
    return payload


def search_web(query: str, page: int = 1) -> dict[str, Any]:
    params = urllib.parse.urlencode({"q": query, "type": "code", "p": str(page)})
    url = f"https://github.com/search?{params}"
    dprint("search_web request", {"page": page, "query": query, "url": url})
    req = urllib.request.Request(
        url,
        headers={
            "Accept": "text/html",
            "User-Agent": USER_AGENT,
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            html = resp.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as e:
        body = e.read().decode("utf-8", errors="replace")[:500]
        dprint("search_web error", {"status": e.code, "query": query, "message": body})
        raise RuntimeError(f"GitHub web search failed (HTTP {e.code}): {body}")

    hrefs = re.findall(r'href="(/[^"]+/blob/[^"#?]+)"', html)
    items: list[dict[str, Any]] = []
    seen: set[str] = set()
    for href in hrefs:
        html_url = urllib.parse.urljoin("https://github.com", unescape(href))
        if html_url in seen:
            continue
        seen.add(html_url)
        parsed = urllib.parse.urlparse(html_url)
        parts = parsed.path.strip("/").split("/")
        if len(parts) < 5 or parts[2] != "blob":
            continue
        repo_full = f"{parts[0]}/{parts[1]}"
        path = urllib.parse.unquote("/".join(parts[4:]))
        items.append(
            {
                "repository": {"full_name": repo_full},
                "path": path,
                "html_url": html_url,
                "url": "",
                "score": 0.0,
            }
        )
        if len(items) >= WEB_PER_PAGE:
            break

    dprint("search_web response", {"page": page, "query": query, "items": len(items)})
    return {"total_count": len(items), "items": items}


def extract_github_blob_url(url: str) -> str | None:
    try:
        parsed = urllib.parse.urlparse(url)
    except ValueError:
        return None
    if parsed.netloc not in {"github.com", "www.github.com"}:
        return None
    path = urllib.parse.unquote(parsed.path)
    if "/raw/" in path:
        path = path.replace("/raw/", "/blob/", 1)
    if "/blob/" not in path:
        return None
    parts = path.strip("/").split("/")
    if len(parts) < 5 or parts[2] != "blob":
        return None
    encoded_path = "/".join(urllib.parse.quote(part, safe="") for part in parts)
    return f"https://github.com/{encoded_path}"


def item_from_blob_url(html_url: str) -> dict[str, Any] | None:
    normalized = extract_github_blob_url(html_url)
    if not normalized:
        return None
    parsed = urllib.parse.urlparse(normalized)
    parts = parsed.path.strip("/").split("/")
    repo_full = f"{parts[0]}/{parts[1]}"
    path = urllib.parse.unquote("/".join(parts[4:]))
    return {
        "repository": {"full_name": repo_full},
        "path": path,
        "html_url": normalized,
        "url": "",
        "score": 0.0,
    }


def item_from_raw_github_url(raw_url: str) -> dict[str, Any] | None:
    try:
        parsed = urllib.parse.urlparse(raw_url)
    except ValueError:
        return None
    if parsed.netloc != "raw.githubusercontent.com":
        return None
    parts = urllib.parse.unquote(parsed.path).strip("/").split("/")
    if len(parts) < 4:
        return None
    owner, repo, ref = parts[0], parts[1], parts[2]
    path = "/".join(parts[3:])
    encoded_parts = [urllib.parse.quote(part, safe="") for part in [owner, repo, "blob", ref, *path.split("/")]]
    html_url = "https://github.com/" + "/".join(encoded_parts)
    return {
        "repository": {"full_name": f"{owner}/{repo}"},
        "path": path,
        "html_url": html_url,
        "url": "",
        "score": 0.0,
    }


def item_from_github_url(url: str) -> dict[str, Any] | None:
    return item_from_blob_url(url) or item_from_raw_github_url(url)


def is_github_listing_url(url: str) -> bool:
    try:
        parsed = urllib.parse.urlparse(url)
    except ValueError:
        return False
    if parsed.netloc not in {"github.com", "www.github.com"}:
        return False
    parts = urllib.parse.unquote(parsed.path).strip("/").split("/")
    if len(parts) < 2:
        return False
    if len(parts) == 2:
        return True
    return len(parts) >= 4 and parts[2] == "tree"


def extract_github_file_links_from_html(base_url: str, html: str) -> list[str]:
    try:
        parsed = urllib.parse.urlparse(base_url)
    except ValueError:
        return []
    base_parts = urllib.parse.unquote(parsed.path).strip("/").split("/")
    if len(base_parts) < 2:
        return []
    owner, repo = base_parts[0], base_parts[1]
    links: list[str] = []
    for raw in re.findall(r'href=["\']([^"\']+)["\']', html, re.I):
        href = unescape(raw).strip()
        if not href:
            continue
        if href.startswith("//"):
            href = "https:" + href
        elif href.startswith("/"):
            href = "https://github.com" + href
        elif href.startswith("?") or href.startswith("#"):
            continue
        elif not href.startswith("http"):
            href = urllib.parse.urljoin(base_url, href)
        try:
            href_parsed = urllib.parse.urlparse(href)
        except ValueError:
            continue
        if href_parsed.netloc not in {"github.com", "www.github.com"}:
            continue
        href_path = urllib.parse.unquote(href_parsed.path)
        if f"/{owner}/{repo}/blob/" in href_path or f"/{owner}/{repo}/raw/" in href_path:
            links.append(href)

    for raw in re.findall(r'https?://raw\.githubusercontent\.com/[^\s"\'<>\\]+', html, re.I):
        links.append(unescape(raw).strip().rstrip(".,);]"))

    deduped: list[str] = []
    seen: set[str] = set()
    for link in links:
        if link and link not in seen:
            deduped.append(link)
            seen.add(link)
    return deduped


def expand_github_listing_url(url: str) -> list[dict[str, Any]]:
    if not is_github_listing_url(url):
        return []
    try:
        html = generic_text_request(
            url,
            {
                "User-Agent": USER_AGENT,
                "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
            },
            timeout=30,
        )
    except Exception as e:
        dprint("search_engine baidu github_listing_error", {"url": url, "error": str(e)})
        return []
    links = extract_github_file_links_from_html(url, html)
    items: list[dict[str, Any]] = []
    for link in links:
        mapped = item_from_github_url(link)
        if mapped:
            items.append(mapped)
    dprint(
        "search_engine baidu github_listing",
        {"url": url, "file_link_count": len(links), "mapped": len(items), "samples": links[:5]},
    )
    return items


def normalize_request_url(url: str) -> str:
    parsed = urllib.parse.urlparse(url)
    path = urllib.parse.quote(urllib.parse.unquote(parsed.path), safe="/:@")
    query = urllib.parse.quote(urllib.parse.unquote(parsed.query), safe="=&/:@")
    fragment = urllib.parse.quote(urllib.parse.unquote(parsed.fragment), safe="")
    return urllib.parse.urlunparse(
        (parsed.scheme, parsed.netloc, path, parsed.params, query, fragment)
    )


def build_raw_url_candidates_from_blob(html_url: str) -> list[str]:
    normalized = normalize_request_url(html_url)
    parsed = urllib.parse.urlparse(normalized)
    parts = parsed.path.strip("/").split("/")
    if len(parts) < 5 or parts[2] != "blob":
        return []

    owner = parts[0]
    repo = parts[1]
    ref = parts[3]
    file_path = "/".join(parts[4:])
    candidates = [
        f"https://github.com/{owner}/{repo}/raw/{ref}/{file_path}",
        f"https://raw.githubusercontent.com/{owner}/{repo}/{ref}/{file_path}",
    ]
    deduped: list[str] = []
    seen: set[str] = set()
    for candidate in candidates:
        candidate = normalize_request_url(candidate)
        if candidate not in seen:
            deduped.append(candidate)
            seen.add(candidate)
    return deduped


def generic_json_request(url: str, headers: dict[str, str] | None = None) -> dict[str, Any]:
    req = urllib.request.Request(url, headers=headers or {"User-Agent": USER_AGENT})
    with urllib.request.urlopen(req, timeout=120) as resp:
        return json.loads(resp.read().decode("utf-8"))


def generic_text_request(url: str, headers: dict[str, str] | None = None, timeout: int = 30) -> str:
    req = urllib.request.Request(url, headers=headers or {"User-Agent": USER_AGENT})
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        raw = resp.read()
        content_type = resp.headers.get("Content-Type", "")
        match = re.search(r"charset=([\w.-]+)", content_type, re.I)
        encoding = match.group(1) if match else "utf-8"
        return raw.decode(encoding, errors="ignore")


def search_engine_query(keyword: str) -> str:
    return keyword


def resolve_baidu_result_url(url: str) -> str:
    if not url:
        return ""
    try:
        req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT}, method="HEAD")
        with urllib.request.urlopen(req, timeout=20) as resp:
            return resp.geturl()
    except Exception:
        try:
            req = urllib.request.Request(url, headers={"User-Agent": USER_AGENT})
            with urllib.request.urlopen(req, timeout=20) as resp:
                return resp.geturl()
        except Exception as e:
            dprint("search_engine baidu resolve_error", {"url": url, "error": str(e)})
            return url


def extract_baidu_result_links(html: str) -> list[str]:
    links: list[str] = []
    for raw in re.findall(r'href=["\']([^"\']+)["\']', html, re.I):
        link = unescape(raw).strip()
        if not link:
            continue
        if link.startswith("//"):
            link = "https:" + link
        elif link.startswith("/"):
            link = "https://www.baidu.com" + link
        if "baidu.com/link?" in link or "github.com/" in link:
            links.append(link)

    for raw in re.findall(r'"url"\s*:\s*"([^"]+)"', html, re.I):
        link = unescape(raw).encode("utf-8").decode("unicode_escape", errors="ignore").strip()
        if link and ("baidu.com/link?" in link or "github.com/" in link):
            links.append(link)

    for raw in re.findall(r'https?://(?:www\.)?github\.com/[^\s"\'<>\\]+', html, re.I):
        link = unescape(raw).encode("utf-8").decode("unicode_escape", errors="ignore").strip()
        link = link.rstrip(".,);]")
        if link:
            links.append(link)

    for raw in re.findall(r'https?://raw\.githubusercontent\.com/[^\s"\'<>\\]+', html, re.I):
        link = unescape(raw).encode("utf-8").decode("unicode_escape", errors="ignore").strip()
        link = link.rstrip(".,);]")
        if link:
            links.append(link)

    deduped: list[str] = []
    seen: set[str] = set()
    for link in links:
        if link not in seen:
            deduped.append(link)
            seen.add(link)
    return deduped


def baidu_queries(query: str) -> list[str]:
    candidates = [
        f"site:github.com {query}",
        f"site:github.com inurl:blob {query}",
        f"site:github.com {query} pdf OR epub OR mobi OR txt",
        f"{query} github.com/blob",
        f"{query} raw.githubusercontent.com",
    ]
    deduped: list[str] = []
    seen: set[str] = set()
    for candidate in candidates:
        candidate = re.sub(r"\s+", " ", candidate).strip()
        if candidate and candidate not in seen:
            deduped.append(candidate)
            seen.add(candidate)
    return deduped


def search_engine_baidu(query: str) -> list[dict[str, Any]]:
    results: list[dict[str, Any]] = []
    seen: set[tuple[str, str]] = set()
    query_summaries: list[str] = []
    all_skipped_samples: list[dict[str, str]] = []
    queries = baidu_queries(query)
    headers = {
        "User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 "
        "(KHTML, like Gecko) Chrome/122.0 Safari/537.36",
        "Accept": "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
        "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
        "Referer": "https://www.baidu.com/",
    }
    for query_index, baidu_query in enumerate(queries, start=1):
        params = urllib.parse.urlencode({"wd": baidu_query, "rn": str(ENGINE_RESULTS)})
        url = f"https://www.baidu.com/s?{params}"
        dprint("search_engine baidu request", {"query_index": query_index, "query": baidu_query, "url": url})
        html = generic_text_request(url, headers)
        links = extract_baidu_result_links(html)
        title_match = re.search(r"<title>(.*?)</title>", html, re.I | re.S)
        title = unescape(re.sub(r"\s+", " ", title_match.group(1))).strip() if title_match else ""
        dprint(
            "search_engine baidu html",
            {
                "query_index": query_index,
                "bytes": len(html.encode("utf-8", errors="ignore")),
                "title": title[:120],
                "raw_link_count": len(links),
                "raw_link_samples": links[:5],
            },
        )
        mapped_count = 0
        expanded_count = 0
        skipped_samples: list[dict[str, str]] = []
        for index, link in enumerate(links, start=1):
            resolved = resolve_baidu_result_url(link) if "baidu.com/link?" in link else link
            mapped_items = []
            mapped = item_from_github_url(resolved)
            if mapped:
                mapped_items = [mapped]
            elif is_github_listing_url(resolved):
                mapped_items = expand_github_listing_url(resolved)
                expanded_count += len(mapped_items)

            if not mapped_items:
                if len(skipped_samples) < 5:
                    skipped_samples.append({"link": link[:180], "resolved": resolved[:180], "reason": "not_github_blob_or_raw"})
                continue
            for mapped in mapped_items:
                key = ((mapped.get("repository") or {}).get("full_name") or "", mapped.get("path") or "")
                if key in seen:
                    continue
                seen.add(key)
                mapped["score"] = float((len(queries) - query_index + 1) * 100 + (ENGINE_RESULTS - index + 1))
                results.append(mapped)
                mapped_count += 1
                if len(results) >= ENGINE_RESULTS:
                    break
            if len(results) >= ENGINE_RESULTS:
                break
        query_summaries.append(
            f"#{query_index} title={title[:40]!r} raw_links={len(links)} mapped={mapped_count} expanded={expanded_count}"
        )
        all_skipped_samples.extend(skipped_samples[:2])
        dprint(
            "search_engine baidu query_result",
            {
                "query_index": query_index,
                "mapped": mapped_count,
                "expanded": expanded_count,
                "total_results": len(results),
                "skipped_samples": skipped_samples,
            },
        )
        if len(results) >= ENGINE_RESULTS:
            break
    dprint("search_engine baidu response", {"query": query, "items": len(results)})
    if not results:
        sample_text = "; ".join(
            f"{s.get('reason')} resolved={s.get('resolved')}" for s in all_skipped_samples[:5]
        )
        raise RuntimeError(
            "Baidu returned 0 GitHub file candidates. "
            f"query_summaries={' | '.join(query_summaries)}. "
            f"skipped_samples={sample_text or 'none'}. "
            "Run with GETGITFILE_DEBUG=1 for full link samples."
        )
    return results


def search_engine_serpapi(query: str) -> list[dict[str, Any]]:
    api_key = os.environ.get("SERPAPI_KEY", "").strip()
    if not api_key:
        return []
    params = urllib.parse.urlencode(
        {
            "engine": "google",
            "q": query,
            "num": str(ENGINE_RESULTS),
            "api_key": api_key,
        }
    )
    url = f"https://serpapi.com/search.json?{params}"
    dprint("search_engine serpapi request", {"query": query, "url": url.replace(api_key, "***")})
    payload = generic_json_request(url, {"User-Agent": USER_AGENT})
    results: list[dict[str, Any]] = []
    for item in payload.get("organic_results") or []:
        link = item.get("link") or ""
        mapped = item_from_blob_url(link)
        if mapped:
            mapped["score"] = float(item.get("position") or 0.0)
            results.append(mapped)
    dprint("search_engine serpapi response", {"query": query, "items": len(results)})
    return results


def search_engine_brave(query: str) -> list[dict[str, Any]]:
    api_key = os.environ.get("BRAVE_SEARCH_API_KEY", "").strip()
    if not api_key:
        return []
    params = urllib.parse.urlencode({"q": query, "count": str(ENGINE_RESULTS)})
    url = f"https://api.search.brave.com/res/v1/web/search?{params}"
    dprint("search_engine brave request", {"query": query, "url": url})
    payload = generic_json_request(
        url,
        {
            "Accept": "application/json",
            "Accept-Encoding": "gzip",
            "X-Subscription-Token": api_key,
            "User-Agent": USER_AGENT,
        },
    )
    results: list[dict[str, Any]] = []
    for item in ((payload.get("web") or {}).get("results") or []):
        link = item.get("url") or ""
        mapped = item_from_blob_url(link)
        if mapped:
            mapped["score"] = float(item.get("page_age") is not None)
            results.append(mapped)
    dprint("search_engine brave response", {"query": query, "items": len(results)})
    return results


def search_engine(keyword: str) -> tuple[str, list[dict[str, Any]]]:
    query = search_engine_query(keyword)
    providers = [
        ("baidu", search_engine_baidu),
        # ("serpapi", search_engine_serpapi),
        # ("brave", search_engine_brave),
    ]
    errors: list[str] = []
    for provider_name, fn in providers:
        try:
            items = fn(query)
        except Exception as e:
            errors.append(f"{provider_name}: {e}")
            dprint("search_engine provider_error", {"provider": provider_name, "error": str(e)})
            continue
        if items:
            return provider_name, items
    if errors:
        raise RuntimeError("; ".join(errors))
    raise RuntimeError(
        "No search-engine provider returned results. Baidu is tried first; optionally set SERPAPI_KEY or BRAVE_SEARCH_API_KEY."
    )


def extension_of(path: str) -> str:
    return os.path.splitext(path)[1].lower()


def should_skip_binary(path: str, data: bytes) -> bool:
    ext = extension_of(path)
    if ext in BINARY_SAFE_EXTENSIONS:
        return False
    return is_probably_binary(data)


def compute_match_score(keyword: str, path: str) -> float:
    basename = os.path.basename(path)
    norm_keyword = normalize_for_match(keyword)
    norm_base = normalize_for_match(basename)
    norm_path = normalize_for_match(path)
    terms = extract_query_terms(keyword)
    ext = extension_of(path)

    score = 0.0

    if norm_keyword and norm_keyword in norm_base:
        score += 180
    elif norm_keyword and norm_keyword in norm_path:
        score += 120

    matched_terms = 0
    for term in terms:
        norm_term = normalize_for_match(term)
        if not norm_term:
            continue
        if norm_term in norm_base:
            score += 60 + min(len(norm_term) * 2, 20)
            matched_terms += 1
        elif norm_term in norm_path:
            score += 30 + min(len(norm_term), 10)
            matched_terms += 1

    if terms:
        score += matched_terms * 15
        score += (matched_terms / len(terms)) * 60

    if norm_keyword and norm_base:
        score += SequenceMatcher(None, norm_keyword, norm_base).ratio() * 80

    if ext in DOCUMENT_EXTENSIONS:
        score += 20
    elif ext:
        score -= 5

    # Prefer filenames where the basename, not just directories, carries the signal.
    if basename != path and norm_base:
        score += min(len(norm_base), 40) * 0.2

    return score


def fetch_contents_metadata(token: str, contents_api_url: str) -> dict[str, Any] | None:
    """GET JSON for a file/dir to read size, type, etc."""
    dprint("fetch_metadata request", contents_api_url)
    status, _hdrs, body = github_request(contents_api_url, token, accept="application/vnd.github+json")
    if status != 200:
        dprint("fetch_metadata error", {"url": contents_api_url, "status": status})
        return None
    try:
        payload = json.loads(body.decode("utf-8"))
        dprint(
            "fetch_metadata response",
            {
                "url": contents_api_url,
                "type": payload.get("type"),
                "size": payload.get("size"),
                "name": payload.get("name"),
            },
        )
        return payload
    except json.JSONDecodeError:
        dprint("fetch_metadata decode_error", contents_api_url)
        return None


def fetch_raw_file(token: str, contents_api_url: str) -> bytes | None:
    dprint("fetch_raw request", contents_api_url)
    status, _hdrs, body = github_request(
        contents_api_url,
        token,
        accept="application/vnd.github.v3.raw",
    )
    if status != 200:
        dprint("fetch_raw error", {"url": contents_api_url, "status": status})
        return None
    dprint("fetch_raw response", {"url": contents_api_url, "bytes": len(body)})
    return body


def fetch_raw_file_from_html(html_url: str) -> bytes | None:
    html_url = normalize_request_url(html_url)
    dprint("fetch_raw_html request", html_url)

    # GitHub blob URLs usually have a predictable raw form; try that first so we
    # do not depend on page HTML structure for PDFs and other binary files.
    for raw_url in build_raw_url_candidates_from_blob(html_url):
        dprint("fetch_raw_html raw_candidate", {"html_url": html_url, "raw_url": raw_url})
        raw_req = urllib.request.Request(
            raw_url,
            headers={
                "Accept": "*/*",
                "User-Agent": USER_AGENT,
            },
        )
        try:
            with urllib.request.urlopen(raw_req, timeout=120) as resp:
                body = resp.read()
            dprint("fetch_raw_html raw_candidate_success", {"url": raw_url, "bytes": len(body)})
            return body
        except urllib.error.HTTPError as e:
            dprint("fetch_raw_html raw_candidate_error", {"url": raw_url, "status": e.code})

    req = urllib.request.Request(
        html_url,
        headers={
            "Accept": "text/html",
            "User-Agent": USER_AGENT,
        },
    )
    try:
        with urllib.request.urlopen(req, timeout=120) as resp:
            html = resp.read().decode("utf-8", errors="replace")
    except urllib.error.HTTPError as e:
        dprint("fetch_raw_html page_error", {"url": html_url, "status": e.code})
        return None

    patterns = [
        r'"rawUrl":"([^"]+)"',
        r'href="(https://raw\.githubusercontent\.com/[^"]+)"',
        r'href="(/[^"]+/raw/[^"]+)"',
    ]
    raw_url = None
    for pattern in patterns:
        match = re.search(pattern, html)
        if match:
            raw_url = unescape(match.group(1)).replace("\\u0026", "&")
            break
    if not raw_url:
        dprint("fetch_raw_html no_raw_url", html_url)
        return None

    raw_url = normalize_request_url(urllib.parse.urljoin("https://github.com", raw_url))
    dprint("fetch_raw_html raw_url", {"html_url": html_url, "raw_url": raw_url})
    raw_req = urllib.request.Request(
        raw_url,
        headers={
            "Accept": "*/*",
            "User-Agent": USER_AGENT,
        },
    )
    try:
        with urllib.request.urlopen(raw_req, timeout=120) as resp:
            body = resp.read()
    except urllib.error.HTTPError as e:
        dprint("fetch_raw_html raw_error", {"url": raw_url, "status": e.code})
        return None

    dprint("fetch_raw_html response", {"url": raw_url, "bytes": len(body)})
    return body


def search_with_strategy(
    mode: str,
    token: str,
    search_queries: list[str],
    keyword: str,
) -> tuple[list[dict[str, Any]], list[dict[str, Any]], list[str]]:
    global_seen: set[tuple[str, str]] = set()
    candidate_items: list[dict[str, Any]] = []
    query_totals: list[dict[str, Any]] = []
    attempted_modes: list[str] = []

    def append_deduped(batch: list[dict[str, Any]], source_query: str, source_mode: str) -> None:
        added = 0
        for it in batch:
            repo = (it.get("repository") or {}).get("full_name") or ""
            path = it.get("path") or ""
            if not repo or not path:
                continue
            key = (repo, path)
            if key in global_seen:
                continue
            global_seen.add(key)
            it["_source_query"] = source_query
            it["_source_mode"] = source_mode
            it["_local_score"] = compute_match_score(keyword, path)
            candidate_items.append(it)
            added += 1
        dprint(
            "append_deduped",
            {
                "source_mode": source_mode,
                "source_query": source_query,
                "batch_size": len(batch),
                "added": added,
                "candidate_total": len(candidate_items),
            },
        )

    def run_api() -> None:
        if not token:
            dprint("run_api skipped", "missing token")
            return
        attempted_modes.append("api")
        api_rate_limited = False
        for query_index, query in enumerate(search_queries):
            if api_rate_limited or len(candidate_items) >= API_TARGET_CANDIDATES:
                break
            for page in range(1, MAX_PAGES_PER_QUERY + 1):
                try:
                    payload = search_code(token, query, page=page, per_page=PER_PAGE)
                except Exception as e:
                    if is_rate_limit_error(e):
                        api_rate_limited = True
                        eprint(f"API rate limit hit for {query!r}; stopping API search and using what we have.")
                        dprint("run_api rate_limited", {"query": query, "page": page, "error": str(e)})
                        break
                    if page == 1:
                        eprint(f"Search error for {query!r}: {e}")
                    else:
                        eprint(f"Search pagination error ({query!r}, page {page}): {e}")
                    break
                if page == 1:
                    query_totals.append(
                        {"mode": "api", "query": query, "total_count": int(payload.get("total_count") or 0)}
                    )
                batch = list(payload.get("items") or [])
                if not batch:
                    break
                append_deduped(batch, query, "api")
                if len(batch) < PER_PAGE or len(candidate_items) >= API_TARGET_CANDIDATES:
                    break
            if query_index == 0 and mode == "api" and not candidate_items:
                dprint("run_api first_query_empty", query)
            if api_rate_limited:
                break

    def run_web() -> None:
        attempted_modes.append("web")
        web_queries: list[str] = []
        for query in search_queries:
            simplified = re.sub(r"\bfilename:", "", query).replace('"', "").strip()
            simplified = re.sub(r"\s+", " ", simplified)
            if simplified and simplified not in web_queries:
                web_queries.append(simplified)
        for query in web_queries[:MAX_QUERIES]:
            for page in range(1, WEB_MAX_PAGES + 1):
                try:
                    payload = search_web(query, page=page)
                except Exception as e:
                    if page == 1:
                        eprint(f"Web search error for {query!r}: {e}")
                    else:
                        eprint(f"Web search pagination error ({query!r}, page {page}): {e}")
                    break
                if page == 1:
                    query_totals.append(
                        {"mode": "web", "query": query, "total_count": int(payload.get("total_count") or 0)}
                    )
                batch = list(payload.get("items") or [])
                if not batch:
                    break
                append_deduped(batch, query, "web")
                if len(batch) < WEB_PER_PAGE:
                    break

    def run_engine() -> None:
        attempted_modes.append("engine")
        provider_name, items = search_engine(keyword)
        query_totals.append({"mode": "engine", "query": search_engine_query(keyword), "provider": provider_name, "total_count": len(items)})
        append_deduped(items, search_engine_query(keyword), "engine")

    def should_expand_to_web() -> bool:
        if not candidate_items:
            return True
        if len(re.split(r"\s+", keyword.strip())) < 2:
            return False
        top_items = candidate_items[:5]
        top_score = max(float(it.get("_local_score") or 0.0) for it in top_items)
        has_document = any(extension_of(it.get("path") or "") in DOCUMENT_EXTENSIONS for it in top_items)
        return top_score < 260 or not has_document

    if mode == "api":
        run_api()
    elif mode == "web":
        run_web()
    elif mode == "engine":
        run_engine()
    else:
        if len(re.split(r"\s+", keyword.strip())) >= 2:
            try:
                run_engine()
            except Exception as e:
                eprint(f"Search-engine mode unavailable: {e}")
                dprint("auto engine unavailable", str(e))
            if not candidate_items:
                run_api()
            if should_expand_to_web():
                dprint(
                    "auto_fallback",
                    {"reason": "engine_or_api_results_weak_or_missing", "candidate_count": len(candidate_items)},
                )
                run_web()
        else:
            run_api()
            if should_expand_to_web():
                dprint("auto_fallback", {"reason": "api_results_weak_or_missing", "candidate_count": len(candidate_items)})
                run_web()

    candidate_items.sort(
        key=lambda it: (
            float(it.get("_local_score") or 0.0),
            float(it.get("score") or 0.0),
        ),
        reverse=True,
    )
    return candidate_items, query_totals, attempted_modes


def allocate_filename(used: set[str], desired: str) -> str:
    if desired not in used:
        used.add(desired)
        return desired
    stem, ext = os.path.splitext(desired)
    n = 2
    while True:
        candidate = f"{stem}_{n}{ext}"
        if candidate not in used:
            used.add(candidate)
            return candidate
        n += 1


def main() -> int:
    try:
        mode, keyword, output_dir_arg = parse_args(sys.argv)
    except ValueError as e:
        eprint(str(e))
        return 2
    try:
        search_queries = build_search_queries(keyword)
    except ValueError as e:
        eprint(f"Invalid keyword: {e}")
        return 2

    token = os.environ.get("GITHUB_TOKEN", "").strip()
    if mode == "api" and not token:
        eprint(
            "Missing GITHUB_TOKEN. Set a personal access token in the environment, e.g.\n"
            "  export GITHUB_TOKEN=ghp_...\n"
            "Code search requires authentication."
        )
        return 1

    dprint(
        "startup",
        {
            "mode": mode,
            "keyword": keyword,
            "query_count": len(search_queries),
            "queries": search_queries,
            "token_prefix": token[:6] + "..." if token and len(token) >= 6 else ("***" if token else ""),
            "max_results": MAX_RESULTS,
            "max_pages_per_query": MAX_PAGES_PER_QUERY,
            "max_file_bytes": MAX_FILE_BYTES,
        },
    )

    script_dir = os.path.dirname(os.path.abspath(__file__))
    if output_dir_arg:
        out_root = os.path.abspath(os.path.expanduser(output_dir_arg))
    else:
        out_dir_name = safe_dir_name(keyword)
        out_root = os.path.join(script_dir, out_dir_name)
    os.makedirs(out_root, exist_ok=True)

    candidate_items, query_totals, attempted_modes = search_with_strategy(
        mode, token, search_queries, keyword
    )

    if not candidate_items:
        eprint(
            "No files matched any search strategy. Try shorter keywords, alternate spellings, "
            "or separate title and author with spaces."
        )
        result_path = os.path.join(out_root, "result.json")
        with open(result_path, "w", encoding="utf-8") as f:
            json.dump(
                {
                    "mode": mode,
                    "attempted_modes": attempted_modes,
                    "keyword": keyword,
                    "github_search_queries": search_queries,
                    "query_totals": query_totals,
                    "output_dir": out_root,
                    "total_count": 0,
                    "downloads": [],
                    "note": "No search hits",
                },
                f,
                indent=2,
                ensure_ascii=False,
            )
        eprint(f"Wrote {result_path}")
        return 0

    candidate_items.sort(
        key=lambda it: (
            float(it.get("_local_score") or 0.0),
            float(it.get("score") or 0.0),
        ),
        reverse=True,
    )
    dprint(
        "candidate_top",
        [
            {
            "path": it.get("path"),
            "repo": (it.get("repository") or {}).get("full_name"),
            "local_score": round(float(it.get("_local_score") or 0.0), 3),
            "api_score": it.get("score"),
            "source_query": it.get("_source_query"),
            "source_mode": it.get("_source_mode"),
        }
        for it in candidate_items[: min(10, len(candidate_items))]
        ],
    )

    used_names: set[str] = set()
    downloads: list[dict[str, Any]] = []
    skipped: list[dict[str, Any]] = []
    rank = 0
    for it in candidate_items:
        if len(downloads) >= MAX_RESULTS:
            break

        repo_full = (it.get("repository") or {}).get("full_name") or ""
        path = it.get("path") or ""
        html_url = it.get("html_url") or ""
        api_url = it.get("url") or ""
        score = it.get("score")
        source_mode = it.get("_source_mode") or "api"
        dprint(
            "candidate_check",
            {
                "repo": repo_full,
                "path": path,
                "api_url": api_url,
                "html_url": html_url,
                "source_mode": source_mode,
                "local_score": round(float(it.get("_local_score") or 0.0), 3),
                "api_score": score,
            },
        )

        if source_mode in {"web", "engine"}:
            raw = fetch_raw_file_from_html(html_url) if html_url else None
            if raw is None:
                dprint("skip", {"repo": repo_full, "path": path, "reason": f"{source_mode} raw download failed"})
                skipped.append({"repository": repo_full, "path": path, "reason": f"{source_mode} raw download failed"})
                continue
            size = len(raw)
            if size > MAX_FILE_BYTES:
                dprint("skip", {"repo": repo_full, "path": path, "reason": "downloaded size exceeds limit", "size": size})
                skipped.append(
                    {
                        "repository": repo_full,
                        "path": path,
                        "reason": f"downloaded size {size} exceeds limit",
                    }
                )
                continue
        else:
            if not api_url:
                dprint("skip", {"repo": repo_full, "path": path, "reason": "missing Contents API url"})
                skipped.append(
                    {"repository": repo_full, "path": path, "reason": "missing Contents API url"}
                )
                continue

            meta = fetch_contents_metadata(token, api_url)
            if not meta:
                dprint("skip", {"repo": repo_full, "path": path, "reason": "could not fetch metadata"})
                skipped.append(
                    {"repository": repo_full, "path": path, "reason": "could not fetch metadata"}
                )
                continue

            if meta.get("type") != "file":
                dprint("skip", {"repo": repo_full, "path": path, "reason": "not a file", "type": meta.get("type")})
                skipped.append({"repository": repo_full, "path": path, "reason": "not a file (dir/submodule)"})
                continue

            size = int(meta.get("size") or 0)
            if size > MAX_FILE_BYTES:
                dprint("skip", {"repo": repo_full, "path": path, "reason": "file too large", "size": size})
                skipped.append(
                    {
                        "repository": repo_full,
                        "path": path,
                        "reason": f"file too large ({size} bytes > {MAX_FILE_BYTES})",
                    }
                )
                continue

            raw = fetch_raw_file(token, api_url)
            if raw is None:
                dprint("skip", {"repo": repo_full, "path": path, "reason": "raw download failed"})
                skipped.append({"repository": repo_full, "path": path, "reason": "raw download failed"})
                continue

            if len(raw) > MAX_FILE_BYTES:
                dprint("skip", {"repo": repo_full, "path": path, "reason": "downloaded size exceeds limit", "size": len(raw)})
                skipped.append(
                    {
                        "repository": repo_full,
                        "path": path,
                        "reason": f"downloaded size {len(raw)} exceeds limit",
                    }
                )
                continue

        if not api_url and source_mode not in {"web", "engine"}:
            dprint("skip", {"repo": repo_full, "path": path, "reason": "missing Contents API url"})
            skipped.append(
                {"repository": repo_full, "path": path, "reason": "missing Contents API url"}
            )
            continue

        if should_skip_binary(path, raw):
            dprint("skip", {"repo": repo_full, "path": path, "reason": "likely unsupported binary content"})
            skipped.append({"repository": repo_full, "path": path, "reason": "likely unsupported binary content"})
            continue

        rank += 1
        local_base = safe_local_filename(path)
        local_name = allocate_filename(used_names, local_base)
        dest = os.path.join(out_root, local_name)

        try:
            with open(dest, "wb") as f:
                f.write(raw)
        except OSError as e:
            skipped.append({"repository": repo_full, "path": path, "reason": f"write failed: {e}"})
            continue

        downloads.append(
            {
                "order": rank,
                "repository": repo_full,
                "path": path,
                "original_url": html_url,
                "api_url": api_url,
                "source_mode": source_mode,
                "search_score": score,
                "local_match_score": round(float(it.get("_local_score") or 0.0), 3),
                "matched_via_query": it.get("_source_query"),
                "local_filename": local_name,
                "bytes": len(raw),
            }
        )
        dprint("downloaded", {"rank": rank, "repo": repo_full, "path": path, "bytes": len(raw), "saved_as": local_name})

    manifest = {
        "mode": mode,
        "attempted_modes": attempted_modes,
        "keyword": keyword,
        "github_search_queries": search_queries,
        "query_totals": query_totals,
        "output_dir": out_root,
        "total_count": len(candidate_items),
        "candidate_count": len(candidate_items),
        "downloaded": len(downloads),
        "downloads": downloads,
        "skipped": skipped,
    }
    result_path = os.path.join(out_root, "result.json")
    with open(result_path, "w", encoding="utf-8") as f:
        json.dump(manifest, f, indent=2, ensure_ascii=False)
    dprint(
        "done",
        {
            "output_dir": out_root,
            "downloaded": len(downloads),
            "skipped": len(skipped),
            "result_path": result_path,
        },
    )

    print(f"Output directory: {out_root}")
    print(f"Downloaded {len(downloads)} file(s). Manifest: {result_path}")
    if skipped:
        print(f"Skipped {len(skipped)} candidate(s) (see result.json -> skipped).")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
