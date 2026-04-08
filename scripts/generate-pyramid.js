#!/usr/bin/env node

/**
 * Pyramid Summary Generator - Prototype
 *
 * Generates hierarchical summaries (L1/L2/L3) for markdown documents.
 *
 * Usage: node scripts/generate-pyramid.js <file-path>
 * Example: node scripts/generate-pyramid.js notes/memory-health.md
 *
 * Output: Creates .cache/<filename>.pyramid.json with all summary levels
 */

import fs from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// Configuration
const CHUNK_STRATEGY = 'date-sections'; // 'date-sections' or 'fixed-size'
const CHUNK_SIZE = 1000; // lines per chunk for fixed-size strategy
const CACHE_DIR = '.cache';

/**
 * Parse markdown file into sections based on date headers
 * Looks for patterns like "### 2026-02-13" or "## 2026-02-13:"
 */
function chunkByDateSections(content) {
  const lines = content.split('\n');
  const chunks = [];
  let currentChunk = null;

  const dateHeaderRegex = /^###?\s+(\d{4}-\d{2}-\d{2})/;

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const match = line.match(dateHeaderRegex);

    if (match) {
      // Start new chunk
      if (currentChunk) {
        chunks.push(currentChunk);
      }
      currentChunk = {
        id: match[1], // date as chunk ID
        title: line.trim(),
        startLine: i + 1,
        endLine: i + 1,
        content: [line]
      };
    } else if (currentChunk) {
      // Add to current chunk
      currentChunk.content.push(line);
      currentChunk.endLine = i + 1;
    }
  }

  // Add final chunk
  if (currentChunk) {
    chunks.push(currentChunk);
  }

  // Join content arrays back to strings
  return chunks.map(chunk => ({
    ...chunk,
    content: chunk.content.join('\n')
  }));
}

/**
 * Parse markdown file into fixed-size chunks
 */
function chunkByFixedSize(content, chunkSize = CHUNK_SIZE) {
  const lines = content.split('\n');
  const chunks = [];

  for (let i = 0; i < lines.length; i += chunkSize) {
    const chunkLines = lines.slice(i, i + chunkSize);
    chunks.push({
      id: `chunk-${Math.floor(i / chunkSize)}`,
      title: `Lines ${i + 1}-${i + chunkLines.length}`,
      startLine: i + 1,
      endLine: i + chunkLines.length,
      content: chunkLines.join('\n')
    });
  }

  return chunks;
}

/**
 * Generate L1 summaries for each chunk
 * In production: would call LLM API (Claude Haiku)
 * Prototype: generates placeholder summaries with metadata
 */
function generateL1Summaries(chunks) {
  return chunks.map(chunk => {
    // Count key patterns as signals
    const content = chunk.content;
    const criticalMarkers = (content.match(/CRITICAL|MAJOR|P0|🚨/g) || []).length;
    const metrics = (content.match(/\d+%|λ=[\d.]+|score:|entries:/gi) || []).length;
    const actionItems = (content.match(/TODO|BLOCKED|REQUIRES|Next:/gi) || []).length;

    // Extract first substantive line (skip header)
    const lines = content.split('\n').filter(l => l.trim().length > 0);
    const firstLine = lines.length > 1 ? lines[1] : lines[0];

    // Generate simple extractive summary
    const wordCount = content.split(/\s+/).length;
    const lineCount = content.split('\n').length;

    let summary = `${chunk.id}: ${lineCount} lines`;

    if (criticalMarkers > 0) summary += `, ${criticalMarkers} critical markers`;
    if (actionItems > 0) summary += `, ${actionItems} action items`;
    if (metrics > 0) summary += `, ${metrics} metrics`;

    // Add excerpt
    const excerpt = firstLine.substring(0, 100);
    if (excerpt) summary += ` - "${excerpt}${firstLine.length > 100 ? '...' : ''}"`;

    return {
      chunkId: chunk.id,
      title: chunk.title,
      range: `L${chunk.startLine}-L${chunk.endLine}`,
      summary,
      metadata: {
        wordCount,
        lineCount,
        criticalMarkers,
        metrics,
        actionItems
      }
    };
  });
}

/**
 * Generate L2 summary from L1 summaries
 * In production: would call LLM API to synthesize
 * Prototype: generates metadata-based summary
 */
function generateL2Summary(l1Summaries, filePath) {
  const totalChunks = l1Summaries.length;
  const totalLines = l1Summaries.reduce((sum, s) => sum + s.metadata.lineCount, 0);
  const totalCritical = l1Summaries.reduce((sum, s) => sum + s.metadata.criticalMarkers, 0);
  const totalActions = l1Summaries.reduce((sum, s) => sum + s.metadata.actionItems, 0);

  const chunksWithCritical = l1Summaries.filter(s => s.metadata.criticalMarkers > 0);
  const chunksWithActions = l1Summaries.filter(s => s.metadata.actionItems > 0);

  let summary = `This document contains ${totalChunks} sections spanning ${totalLines} lines. `;

  if (totalCritical > 0) {
    summary += `${totalCritical} critical markers found across ${chunksWithCritical.length} sections. `;
  }

  if (totalActions > 0) {
    summary += `${totalActions} action items identified in ${chunksWithActions.length} sections. `;
  }

  // Add temporal info if date-based chunks
  if (l1Summaries[0]?.chunkId.match(/^\d{4}-\d{2}-\d{2}$/)) {
    const dates = l1Summaries.map(s => s.chunkId).sort();
    summary += `Covers ${dates[0]} to ${dates[dates.length - 1]}.`;
  }

  return summary.trim();
}

/**
 * Generate L3 summary (one-line description)
 * In production: would call LLM API
 * Prototype: generates from filename + L2 metadata
 */
function generateL3Summary(filePath, l2Summary) {
  const filename = path.basename(filePath, '.md');
  const prettyName = filename.split('-').map(w => w.charAt(0).toUpperCase() + w.slice(1)).join(' ');

  // Extract key numbers from L2
  const sectionsMatch = l2Summary.match(/(\d+) sections/);
  const dateMatch = l2Summary.match(/(\d{4}-\d{2}-\d{2}) to (\d{4}-\d{2}-\d{2})/);

  if (dateMatch) {
    return `${prettyName}: ${sectionsMatch ? sectionsMatch[1] + ' entries' : 'Log'} from ${dateMatch[1]} to ${dateMatch[2]}`;
  }

  return `${prettyName}: ${sectionsMatch ? sectionsMatch[1] + ' sections' : 'Documentation'}`;
}

/**
 * Main processing pipeline
 */
function generatePyramid(filePath) {
  console.log(`\n📊 Generating pyramid summaries for: ${filePath}\n`);

  // Read file
  if (!fs.existsSync(filePath)) {
    console.error(`❌ File not found: ${filePath}`);
    process.exit(1);
  }

  const content = fs.readFileSync(filePath, 'utf-8');
  const stats = fs.statSync(filePath);

  console.log(`📄 File size: ${(stats.size / 1024).toFixed(1)}KB`);
  console.log(`📝 Total lines: ${content.split('\n').length}\n`);

  // L0: Full document (no processing, just reference)
  const l0 = {
    path: filePath,
    size: stats.size,
    lines: content.split('\n').length,
    modified: stats.mtime.toISOString()
  };

  console.log(`🔍 Chunking strategy: ${CHUNK_STRATEGY}`);

  // Chunk document
  const chunks = CHUNK_STRATEGY === 'date-sections'
    ? chunkByDateSections(content)
    : chunkByFixedSize(content);

  console.log(`✂️  Created ${chunks.length} chunks\n`);

  // L1: Section summaries
  console.log(`📋 Generating L1 summaries (per-chunk)...`);
  const l1 = generateL1Summaries(chunks);
  console.log(`✅ Generated ${l1.length} L1 summaries\n`);

  // L2: Document summary
  console.log(`📄 Generating L2 summary (document overview)...`);
  const l2 = generateL2Summary(l1, filePath);
  console.log(`✅ L2: ${l2}\n`);

  // L3: One-line description
  console.log(`📌 Generating L3 summary (one-line)...`);
  const l3 = generateL3Summary(filePath, l2);
  console.log(`✅ L3: ${l3}\n`);

  // Create pyramid structure
  const pyramid = {
    generated: new Date().toISOString(),
    strategy: CHUNK_STRATEGY,
    l0,
    l3,
    l2,
    l1
  };

  // Write to cache
  const cacheDir = path.join(path.dirname(filePath), CACHE_DIR);
  if (!fs.existsSync(cacheDir)) {
    fs.mkdirSync(cacheDir, { recursive: true });
  }

  const baseName = path.basename(filePath, '.md');
  const cacheFile = path.join(cacheDir, `${baseName}.pyramid.json`);

  fs.writeFileSync(cacheFile, JSON.stringify(pyramid, null, 2));

  console.log(`💾 Cached pyramid to: ${cacheFile}`);
  console.log(`📦 Cache size: ${(fs.statSync(cacheFile).size / 1024).toFixed(1)}KB\n`);

  return pyramid;
}

// CLI execution
if (import.meta.url === `file://${process.argv[1]}`) {
  const args = process.argv.slice(2);

  if (args.length === 0) {
    console.log(`
Usage: node scripts/generate-pyramid.js <file-path>

Example:
  node scripts/generate-pyramid.js notes/memory-health.md

Generates:
  - L0: Full document reference
  - L1: Section-by-section summaries (cached in JSON)
  - L2: Document overview paragraph
  - L3: One-line description

Output: .cache/<filename>.pyramid.json
    `);
    process.exit(0);
  }

  const filePath = args[0];
  const pyramid = generatePyramid(filePath);

  // Print L1 sample
  console.log(`\n📋 Sample L1 summaries (first 3):\n`);
  pyramid.l1.slice(0, 3).forEach(s => {
    console.log(`${s.range}: ${s.summary}`);
  });

  if (pyramid.l1.length > 3) {
    console.log(`... (${pyramid.l1.length - 3} more)`);
  }
}

export { generatePyramid, chunkByDateSections, chunkByFixedSize };
