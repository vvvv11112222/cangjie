"""Validate integration fixtures, local document links and Markdown table structure."""
import json
import re
from pathlib import Path
from urllib.parse import unquote

from jsonschema import Draft202012Validator, FormatChecker

ROOT = Path(__file__).resolve().parents[1]


def main():
    schema = json.loads((ROOT / "contracts/v1.schema.json").read_text(encoding="utf-8"))
    Draft202012Validator.check_schema(schema)
    manifest = json.loads((ROOT / "contracts/examples/manifest.json").read_text(encoding="utf-8"))
    for entry in manifest["examples"]:
        value = json.loads((ROOT / "contracts/examples" / entry["file"]).read_text(encoding="utf-8"))
        selected = {**schema, "$ref": f"#/$defs/{entry['definition']}"}
        errors = list(Draft202012Validator(selected, format_checker=FormatChecker()).iter_errors(value))
        if (not errors) != entry["valid"]:
            raise AssertionError(f"{entry['file']}: unexpected validity; {errors[:1]}")
        if entry["valid"]:
            check_semantics(value)
    docs = [ROOT / "README.md", ROOT / "AGENTS.md", ROOT / "项目文档.md", *sorted((ROOT / "docs").glob("*.md"))]
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
    print(f"PASS: {len(manifest['examples'])} positive/negative contract fixtures, {len(docs)} documents, local links and table structure")


def check_semantics(value):
    if isinstance(value, list):
        for item in value:
            check_semantics(item)
    elif isinstance(value, dict):
        if "start_ms" in value and "end_ms" in value:
            assert value["end_ms"] > value["start_ms"], "invalid interval"
        if "dimensions" in value:
            codes = [d["dimension_code"] for d in value["dimensions"]]
            assert len(set(codes)) == 6, "duplicate/missing report dimension"
        if "segments" in value:
            numbers = [s["segment_no"] for s in value["segments"]]
            assert numbers == list(range(len(numbers))), "non-contiguous segment numbers"
        for item in value.values():
            check_semantics(item)


if __name__ == "__main__":
    main()
