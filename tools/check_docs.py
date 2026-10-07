"""Validate integration fixtures, local document links and Markdown table structure."""
import json
import re
from pathlib import Path
from urllib.parse import unquote

from jsonschema import Draft202012Validator, FormatChecker
from contract_rules import check_semantics

ROOT = Path(__file__).resolve().parents[1]


def main():
    schema = json.loads((ROOT / "contracts/v1.schema.json").read_text(encoding="utf-8"))
    Draft202012Validator.check_schema(schema)
    manifest = json.loads((ROOT / "contracts/examples/manifest.json").read_text(encoding="utf-8"))
    entries = []
    for entry in manifest["examples"]:
        entries.append({**entry, "value": json.loads((ROOT / "contracts/examples" / entry["file"]).read_text(encoding="utf-8"))})
    for case_file in manifest.get("case_sets", []):
        entries.extend(json.loads((ROOT / "contracts/examples" / case_file).read_text(encoding="utf-8")))
    for entry in entries:
        value = entry["value"]
        selected = {**schema, "$ref": f"#/$defs/{entry['definition']}"}
        errors = list(Draft202012Validator(selected, format_checker=FormatChecker()).iter_errors(value))
        if (not errors) != entry["valid"]:
            raise AssertionError(f"{entry.get('file', entry.get('name'))}: unexpected validity; {errors[:1]}")
        if entry["valid"]:
            try:
                check_semantics(value, entry["definition"], entry.get("context"))
                semantic_valid = True
            except ValueError as error:
                semantic_valid = False
                if entry.get("semantic_valid", True):
                    raise AssertionError(f"{entry.get('file', entry.get('name'))}: {error}") from error
            if semantic_valid != entry.get("semantic_valid", True):
                raise AssertionError(f"{entry.get('name')}: semantic failure was not detected")
    catalogue = json.loads((ROOT / "contracts/endpoints.json").read_text(encoding="utf-8"))
    if catalogue["schema_version"] != manifest["schema_version"]:
        raise AssertionError("Endpoint/fixture contract versions differ")
    for name in ("Claim", "ProbeResult", "AudioResult", "VideoResult", "ModelInput"):
        if schema["$defs"][name]["properties"]["schema_version"]["const"] != manifest["schema_version"]:
            raise AssertionError(f"{name}: contract version differs")
    routes = set()
    covered = {e["definition"] for e in entries if e["valid"] and e.get("semantic_valid", True)}
    for endpoint in catalogue["endpoints"]:
        route = (endpoint["method"], endpoint["path"])
        if route in routes:
            raise AssertionError(f"Duplicate route: {route}")
        routes.add(route)
        for field in ("request", "response", "query"):
            name = endpoint[field]
            if name is not None and (name not in schema["$defs"] or name not in covered):
                raise AssertionError(f"{route}: {field} {name} has no definition or positive fixture")
    docs = [ROOT / "README.md", ROOT / "AGENTS.md", ROOT / "项目文档.md", *sorted((ROOT / "docs").glob("*.md"))]
    docs.extend(sorted((ROOT / "workers").glob("*.md")))
    remediation = ROOT / "reviews/整改报告.md"
    if remediation.exists():
        docs.append(remediation)
    for file in docs:
        body = file.read_text(encoding="utf-8")
        in_code = False
        table_width = None
        for number, line in enumerate(body.splitlines(), 1):
            if line.startswith("```"):
                in_code = not in_code
                table_width = None
                continue
            if in_code:
                continue
            if line.startswith("|"):
                width = len(re.split(r"(?<!\\)\|", line)) - 2
                if table_width is not None and width != table_width:
                    raise AssertionError(f"{file.name}:{number}: table has {width} columns, expected {table_width}")
                table_width = width
            else:
                table_width = None
            for target in re.findall(r"\]\(([^)]+)\)", line):
                target = unquote(target.strip("<>"))
                if re.match(r"[a-zA-Z]+://", target):
                    continue
                location, _, fragment = target.partition("#")
                dest = file.parent / location if location else file
                if not dest.exists():
                    raise AssertionError(f"{file.name}:{number}: broken link {target}")
                if fragment and dest.suffix == ".md":
                    headings = re.findall(r"^#{1,6}\s+(.+)$", dest.read_text(encoding="utf-8"), re.M)
                    anchors = [re.sub(r"[^\w\- ]", "", h.lower()).replace(" ", "-") for h in headings]
                    if fragment not in anchors:
                        raise AssertionError(f"{file.name}:{number}: missing heading {target}")
        if in_code:
            raise AssertionError(f"{file.name}: unclosed code fence")
    print(f"PASS: {len(entries)} contract fixtures, {len(routes)} endpoint mappings, {len(docs)} documents, links and tables")


if __name__ == "__main__":
    main()
