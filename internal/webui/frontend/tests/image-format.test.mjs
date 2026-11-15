import assert from "node:assert/strict";
import test from "node:test";
import { UNAVAILABLE, formatImageCreated, formatImageSize, formatImageText, shortDigest, sortedLabels } from "../src/image-format.js";

test("an unavailable image size is never rendered as zero", () => {
  assert.equal(formatImageSize(null), UNAVAILABLE);
  assert.equal(formatImageSize(undefined), UNAVAILABLE);
  assert.equal(formatImageSize(-1), UNAVAILABLE);
  assert.equal(formatImageSize(0), "0 B");
});

test("image sizes use binary units like kuke get image", () => {
  assert.equal(formatImageSize(512), "512 B");
  assert.equal(formatImageSize(1536), "1.5 KiB");
  assert.equal(formatImageSize(3_400_000), "3.2 MiB");
  assert.equal(formatImageSize(5 * 1024 ** 3), "5.0 GiB");
});

test("creation time renders as UTC and unknown times are unavailable", () => {
  assert.equal(formatImageCreated("2026-01-02T03:04:05Z"), "2026-01-02T03:04:05Z");
  assert.equal(formatImageCreated(null), UNAVAILABLE);
  assert.equal(formatImageCreated("0001-01-01T00:00:00Z"), UNAVAILABLE);
  assert.equal(formatImageCreated("not a time"), UNAVAILABLE);
});

test("digests are shortened with their algorithm and empty text is unavailable", () => {
  assert.equal(shortDigest("sha256:0123456789abcdef0123"), "sha256:0123456789ab…");
  assert.equal(shortDigest("sha256:abc"), "sha256:abc");
  assert.equal(shortDigest(""), UNAVAILABLE);
  assert.equal(formatImageText(""), UNAVAILABLE);
  assert.equal(formatImageText("application/vnd.oci.image.index.v1+json"), "application/vnd.oci.image.index.v1+json");
});

test("labels are sorted by key", () => {
  assert.deepEqual(sortedLabels({ b: "2", a: "1" }), [["a", "1"], ["b", "2"]]);
  assert.deepEqual(sortedLabels(null), []);
});
