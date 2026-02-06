// Utilities for deriving structured run progress from researcher.log tail text
// and GitHub issue checklist bodies.

export function stripAnsi(input) {
  if (!input) return '';
  // Broad ANSI escape sequence matcher (colors, cursor control, etc.)
  // eslint-disable-next-line no-control-regex
  return String(input).replace(/\x1B(?:[@-Z\\-_]|\[[0-?]*[ -/]*[@-~])/g, '');
}

export function parseIssueChecklist(body) {
  const text = body || '';
  const lines = String(text).split('\n');
  let done = 0;
  let total = 0;

  for (const line of lines) {
    const m = line.match(/^\s*-\s*\[([xX ])\]\s+/);
    if (!m) continue;
    total += 1;
    if (m[1].toLowerCase() === 'x') done += 1;
  }

  return { done, total };
}

function extractLogMessage(line) {
  if (!line) return '';
  const s = String(line);
  const start = s.indexOf('msg="');
  if (start === -1) return s;
  const from = start + 5;
  const end = s.lastIndexOf('"');
  if (end <= from) return s.slice(from);
  return s.slice(from, end);
}

function findLastMatch(lines, regex) {
  for (let i = lines.length - 1; i >= 0; i -= 1) {
    const line = lines[i];
    const m = line.match(regex);
    if (m) return { match: m, line, index: i };
  }
  return null;
}

export function parseRunProgress(logText) {
  const clean = stripAnsi(logText || '');
  const lines = clean.split('\n').filter(Boolean);

  // Topic iteration loop (only present in topic-iteration mode)
  const topicIt = findLastMatch(
    lines,
    /===\s+Topic\s+#(\d+)\s+iteration\s+(\d+)\/(\d+)\s+===/
  );

  // Current article (new/existing)
  const articleLine = findLastMatch(
    lines,
    /Processing\s+(NEW|EXISTING)\s+article(?:\s+for\s+improvement)?:\s+'([^']+)'/
  );

  // Improvement attempts/successes
  const improvementLine = findLastMatch(
    lines,
    /\[Improvement\s+(\d+)\/(\d+)\s+attempts,\s+(\d+)\/(\d+)\s+successes\]\s+Improving\s+'([^']+)'/
  );

  // Image generation phase (Phase 2) progress
  const imageGenLine = findLastMatch(
    lines,
    /\[Image Generation\]\s+Generating image\s+(\d+)\/(\d+):\s+'([^']+)'(?:\s+candidate\s+(\d+))?/
  );

  // Some runs may not log the topic string in single quotes; keep a fallback matcher.
  const imageGenLineFallback = imageGenLine
    ? null
    : findLastMatch(
        lines,
        /\[Image Generation\]\s+Generating image\s+(\d+)\/(\d+):\s+(.+)$/
      );

  const imageSummary = findLastMatch(
    lines,
    /\[Image Generation\]\s+Completed:\s+(\d+)\s+generated,\s+(\d+)\s+errors/
  );

  // Detect image generation mode even before we see a concrete i/n line.
  const imageModeHint = findLastMatch(
    lines,
    /(Processing images on branch:|Generating images from prompts|\[Image Generation\])/
  );

  const topicIteration = topicIt
    ? {
        topicNumber: Number(topicIt.match[1]),
        current: Number(topicIt.match[2]),
        total: Number(topicIt.match[3]),
        raw: topicIt.line,
        message: extractLogMessage(topicIt.line),
      }
    : null;

  const currentArticle = articleLine
    ? {
        kind: String(articleLine.match[1]).toLowerCase(), // new|existing
        name: articleLine.match[2],
        raw: articleLine.line,
        message: extractLogMessage(articleLine.line),
      }
    : null;

  const improvement = improvementLine
    ? {
        attempt: Number(improvementLine.match[1]),
        maxAttempts: Number(improvementLine.match[2]),
        successes: Number(improvementLine.match[3]),
        minSuccesses: Number(improvementLine.match[4]),
        article: improvementLine.match[5],
        raw: improvementLine.line,
        message: extractLogMessage(improvementLine.line),
      }
    : null;

  let imageGen = null;
  if (imageGenLine) {
    imageGen = {
      current: Number(imageGenLine.match[1]),
      total: Number(imageGenLine.match[2]),
      topic: imageGenLine.match[3],
      candidateIdx: imageGenLine.match[4] ? Number(imageGenLine.match[4]) : null,
      raw: imageGenLine.line,
      message: extractLogMessage(imageGenLine.line),
    };
  } else if (imageGenLineFallback) {
    imageGen = {
      current: Number(imageGenLineFallback.match[1]),
      total: Number(imageGenLineFallback.match[2]),
      topic: String(imageGenLineFallback.match[3]).trim(),
      candidateIdx: null,
      raw: imageGenLineFallback.line,
      message: extractLogMessage(imageGenLineFallback.line),
    };
  } else if (imageModeHint) {
    imageGen = {
      current: null,
      total: null,
      topic: null,
      candidateIdx: null,
      raw: imageModeHint.line,
      message: extractLogMessage(imageModeHint.line),
    };
  }

  const imageCompletion = imageSummary
    ? {
        generated: Number(imageSummary.match[1]),
        errors: Number(imageSummary.match[2]),
        raw: imageSummary.line,
        message: extractLogMessage(imageSummary.line),
      }
    : null;

  // Choose a last event line to display, in priority order
  const lastEvent =
    (imageGen && (imageGen.message || imageGen.raw)) ||
    (improvement && (improvement.message || improvement.raw)) ||
    (topicIteration && (topicIteration.message || topicIteration.raw)) ||
    (currentArticle && (currentArticle.message || currentArticle.raw)) ||
    (lines.length ? extractLogMessage(lines[lines.length - 1]) : null);

  return {
    topicIteration,
    currentArticle,
    improvement,
    imageGen,
    imageCompletion,
    lastEvent,
  };
}

