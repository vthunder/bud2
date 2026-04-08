#!/usr/bin/env node

/**
 * Pyramid Search - Prototype
 *
 * Demonstrates the L3→L2→L1→L0 search funnel pattern.
 * Uses simple text matching (production would use semantic embeddings).
 *
 * Usage: node scripts/search-pyramid.js <cache-file> <query>
 * Example: node scripts/search-pyramid.js notes/.cache/memory-health.pyramid.json "activation decay"
 */

import fs from 'fs';

/**
 * Simple text relevance scoring (production: use embeddings)
 */
function scoreRelevance(text, query) {
  const lowerText = text.toLowerCase();
  const lowerQuery = query.toLowerCase();
  const queryWords = lowerQuery.split(/\s+/);

  let score = 0;

  // Exact phrase match: highest score
  if (lowerText.includes(lowerQuery)) {
    score += 100;
  }

  // Individual word matches
  queryWords.forEach(word => {
    if (word.length < 3) return; // skip short words
    const matches = (lowerText.match(new RegExp(word, 'gi')) || []).length;
    score += matches * 10;
  });

  return score;
}

/**
 * Search pyramid using funnel approach
 */
function searchPyramid(pyramid, query, options = {}) {
  const {
    topL1 = 5, // how many L1 sections to return
    minScore = 10 // minimum relevance score
  } = options;

  console.log(`\n🔍 Searching for: "${query}"\n`);

  // L3: Check document-level relevance
  console.log(`📌 L3 (Document): ${pyramid.l3}`);
  const l3Score = scoreRelevance(pyramid.l3, query);
  console.log(`   Score: ${l3Score}\n`);

  // L2: Check overview relevance
  console.log(`📄 L2 (Overview): ${pyramid.l2}`);
  const l2Score = scoreRelevance(pyramid.l2, query);
  console.log(`   Score: ${l2Score}\n`);

  // L1: Score all sections
  console.log(`📋 L1 (Sections): Scoring ${pyramid.l1.length} sections...`);

  const l1Results = pyramid.l1
    .map(section => ({
      ...section,
      score: scoreRelevance(section.summary + ' ' + section.title, query)
    }))
    .filter(s => s.score >= minScore)
    .sort((a, b) => b.score - a.score)
    .slice(0, topL1);

  console.log(`   Found ${l1Results.length} relevant sections\n`);

  // Display results
  if (l1Results.length === 0) {
    console.log(`❌ No matching sections found (min score: ${minScore})`);
    console.log(`💡 Try a broader query or check L2/L3 summaries above\n`);
    return { l3Score, l2Score, l1Results: [] };
  }

  console.log(`✅ Top ${l1Results.length} matching sections:\n`);

  l1Results.forEach((section, idx) => {
    console.log(`${idx + 1}. ${section.title}`);
    console.log(`   ${section.range} | Score: ${section.score}`);
    console.log(`   ${section.summary}`);

    // Show metadata highlights
    const highlights = [];
    if (section.metadata.criticalMarkers > 0) highlights.push(`${section.metadata.criticalMarkers} critical`);
    if (section.metadata.actionItems > 0) highlights.push(`${section.metadata.actionItems} actions`);
    if (section.metadata.metrics > 0) highlights.push(`${section.metadata.metrics} metrics`);

    if (highlights.length > 0) {
      console.log(`   📊 ${highlights.join(', ')}`);
    }
    console.log();
  });

  // L0: Show how to access full content
  console.log(`📖 L0 (Full Text): To read full content, open:`);
  console.log(`   ${pyramid.l0.path}`);

  if (l1Results.length > 0) {
    console.log(`\n   Jump to lines:`);
    l1Results.slice(0, 3).forEach(r => {
      console.log(`   - ${r.range}: ${r.chunkId}`);
    });
  }
  console.log();

  return { l3Score, l2Score, l1Results };
}

/**
 * Stats summary
 */
function showStats(pyramid) {
  console.log(`\n📊 Pyramid Statistics\n`);
  console.log(`Generated: ${new Date(pyramid.generated).toLocaleString()}`);
  console.log(`Strategy: ${pyramid.strategy}`);
  console.log(`File: ${pyramid.l0.path}`);
  console.log(`Size: ${(pyramid.l0.size / 1024).toFixed(1)}KB`);
  console.log(`Lines: ${pyramid.l0.lines}`);
  console.log(`Sections (L1): ${pyramid.l1.length}`);

  const totalWords = pyramid.l1.reduce((sum, s) => sum + s.metadata.wordCount, 0);
  const avgPerSection = Math.round(totalWords / pyramid.l1.length);
  console.log(`Words: ${totalWords} (avg ${avgPerSection}/section)`);

  const criticalSections = pyramid.l1.filter(s => s.metadata.criticalMarkers > 0);
  const actionSections = pyramid.l1.filter(s => s.metadata.actionItems > 0);

  console.log(`Critical sections: ${criticalSections.length}`);
  console.log(`Action sections: ${actionSections.length}`);
  console.log();
}

// CLI execution
if (import.meta.url === `file://${process.argv[1]}`) {
  const args = process.argv.slice(2);

  if (args.length === 0) {
    console.log(`
Usage: node scripts/search-pyramid.js <cache-file> [query]

Examples:
  node scripts/search-pyramid.js notes/.cache/memory-health.pyramid.json
  node scripts/search-pyramid.js notes/.cache/memory-health.pyramid.json "activation decay"
  node scripts/search-pyramid.js notes/.cache/memory-health.pyramid.json "critical"

Without query: shows pyramid statistics
With query: searches using L3→L2→L1 funnel
    `);
    process.exit(0);
  }

  const cacheFile = args[0];

  if (!fs.existsSync(cacheFile)) {
    console.error(`❌ Cache file not found: ${cacheFile}`);
    console.error(`\nRun generate-pyramid.js first to create the cache.`);
    process.exit(1);
  }

  const pyramid = JSON.parse(fs.readFileSync(cacheFile, 'utf-8'));

  if (args.length === 1) {
    // No query: show stats
    showStats(pyramid);
  } else {
    // Query provided: search
    const query = args.slice(1).join(' ');
    const results = searchPyramid(pyramid, query, {
      topL1: 5,
      minScore: 10
    });

    // Summary
    console.log(`\n📈 Search Summary:`);
    console.log(`   L3 relevance: ${results.l3Score}`);
    console.log(`   L2 relevance: ${results.l2Score}`);
    console.log(`   L1 matches: ${results.l1Results.length}`);
    console.log();
  }
}

export { searchPyramid, scoreRelevance };
