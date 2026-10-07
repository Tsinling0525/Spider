import DOMPurify from "dompurify";
import { marked } from "marked";

export function renderMarkdown(text: string): string {
  return DOMPurify.sanitize(marked.parse(text, { async: false, breaks: true, gfm: true }), {
    FORBID_TAGS: ["img", "style", "iframe", "form", "input", "video", "audio"],
    FORBID_ATTR: ["style"],
  });
}
