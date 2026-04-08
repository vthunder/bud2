#!/usr/bin/env node
/**
 * Memory Health Log Rotation Script
 *
 * Rotates dated entries from notes/memory-health.md to monthly archive files.
 * Preserves:
 * - Header and live metrics table
 * - Entries newer than KEEP_DAYS
 * - Entries with critical markers (regardless of age)
 *
 * Usage: node rotate-memory-health.js [--dry-run] [--keep-days N]
 */

const fs = require('fs');
const path = require('path');

// Configuration
const KEEP_DAYS = parseInt(process.argv.find(arg => arg.startsWith('--keep-days='))?.split('=')[1]) || 30;
const DRY_RUN = process.argv.includes('--dry-run');
const SOURCE_FILE = path.join(__dirname, '../notes/memory-health.md');
const ARCHIVE_DIR = path.join(__dirname, '../notes/archive');

// Critical markers that preserve entries regardless of age
const CRITICAL_MARKERS = [
  'CRITICAL', 'MAJOR', 'P0', 'URGENT',
  '🚨', '❌', '⚠️',
  'TODO', 'BLOCKED', 'REQUIRES',
  'deployed', 'regression', 'bug fix',
  '> 50%', 'exploded', 'collapsed'
];

/**
 * Parse memory-health.md into sections
 */
function parseFile(filePath) {
  const content = fs.readFileSync(filePath, 'utf8');
  const lines = content.split('\n');

  let header = [];
  let entries = [];
  let currentEntry = null;
  let headerComplete = false;

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];

    // Detect start of Dated Insights section
    if (line.includes('## Dated Insights') || line.match(/^### \d{4}-\d{2}-\d{2}/)) {
      headerComplete = true;
    }

    if (!headerComplete) {
      header.push(line);
      continue;
    }

    // Detect dated entry (### YYYY-MM-DD HH:MM)
    const dateMatch = line.match(/^### (\d{4}-\d{2}-\d{2}) (\d{2}:\d{2})/);
    if (dateMatch) {
      if (currentEntry) {
        entries.push(currentEntry);
      }
      currentEntry = {
        date: new Date(`${dateMatch[1]}T${dateMatch[2]}:00`),
        heading: line,
        content: [line],
        hasCritical: false
      };
    } else if (currentEntry) {
      currentEntry.content.push(line);
      // Check for critical markers
      if (CRITICAL_MARKERS.some(marker => line.includes(marker))) {
        currentEntry.hasCritical = true;
      }
    } else {
      // Lines after header but before first entry
      header.push(line);
    }
  }

  // Add final entry
  if (currentEntry) {
    entries.push(currentEntry);
  }

  return { header, entries };
}

/**
 * Format date as YYYY-MM for archive filenames
 */
function formatMonth(date) {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, '0');
  return `${year}-${month}`;
}

/**
 * Group entries by month
 */
function groupByMonth(entries) {
  const groups = new Map();
  for (const entry of entries) {
    const month = formatMonth(entry.date);
    if (!groups.has(month)) {
      groups.set(month, []);
    }
    groups.get(month).push(entry);
  }
  return groups;
}

/**
 * Main rotation logic
 */
function rotateMemoryHealth() {
  if (!fs.existsSync(SOURCE_FILE)) {
    console.error(`Source file not found: ${SOURCE_FILE}`);
    process.exit(1);
  }

  console.log(`Rotating ${SOURCE_FILE}`);
  console.log(`Keep days: ${KEEP_DAYS}`);
  console.log(`Dry run: ${DRY_RUN}`);
  console.log('');

  // Parse file
  const { header, entries } = parseFile(SOURCE_FILE);
  console.log(`Parsed ${entries.length} dated entries`);

  // Calculate cutoff date
  const cutoffDate = new Date();
  cutoffDate.setDate(cutoffDate.getDate() - KEEP_DAYS);
  console.log(`Cutoff date: ${cutoffDate.toISOString().split('T')[0]}`);

  // Split entries
  const keep = [];
  const archive = [];

  for (const entry of entries) {
    if (entry.date >= cutoffDate || entry.hasCritical) {
      keep.push(entry);
    } else {
      archive.push(entry);
    }
  }

  console.log(`\nKeeping ${keep.length} entries (${keep.filter(e => e.hasCritical).length} marked critical)`);
  console.log(`Archiving ${archive.length} entries`);

  if (archive.length === 0) {
    console.log('\nNo entries to archive. Exiting.');
    return;
  }

  // Group archives by month
  const archivesByMonth = groupByMonth(archive);
  console.log(`\nArchive breakdown by month:`);
  for (const [month, entries] of archivesByMonth) {
    console.log(`  ${month}: ${entries.length} entries`);
  }

  if (DRY_RUN) {
    console.log('\n[DRY RUN] Would write files:');
    for (const [month] of archivesByMonth) {
      console.log(`  - archive/memory-health-${month}.md`);
    }
    console.log(`  - ${SOURCE_FILE} (updated with ${keep.length} entries)`);
    return;
  }

  // Create archive directory if needed
  if (!fs.existsSync(ARCHIVE_DIR)) {
    fs.mkdirSync(ARCHIVE_DIR, { recursive: true });
  }

  // Write archives
  for (const [month, entries] of archivesByMonth) {
    const archiveFile = path.join(ARCHIVE_DIR, `memory-health-${month}.md`);
    const archiveContent = [
      `# Memory Health Archive - ${month}`,
      '',
      `Rotated on ${new Date().toISOString().split('T')[0]}`,
      '',
      '## Dated Insights',
      '',
      ...entries.flatMap(e => e.content),
      ''
    ].join('\n');

    // Append if file exists, create otherwise
    if (fs.existsSync(archiveFile)) {
      const existing = fs.readFileSync(archiveFile, 'utf8');
      fs.writeFileSync(archiveFile, existing + '\n' + archiveContent);
      console.log(`\nAppended to ${path.basename(archiveFile)}`);
    } else {
      fs.writeFileSync(archiveFile, archiveContent);
      console.log(`\nCreated ${path.basename(archiveFile)}`);
    }
  }

  // Rewrite main file
  const newContent = [
    ...header,
    '',
    ...keep.flatMap(e => e.content),
    ''
  ].join('\n');

  fs.writeFileSync(SOURCE_FILE, newContent);
  console.log(`\nUpdated ${path.basename(SOURCE_FILE)} (kept ${keep.length} entries)`);

  // Report statistics
  const oldSize = fs.statSync(SOURCE_FILE).size;
  const keptLines = keep.reduce((sum, e) => sum + e.content.length, 0);
  console.log(`\nFile size reduced from ${Math.round(oldSize / 1024)}KB to ~${Math.round(keptLines * 50 / 1024)}KB (estimated)`);
  console.log('\n✅ Rotation complete!');
}

// Run
try {
  rotateMemoryHealth();
} catch (err) {
  console.error('Error during rotation:', err);
  process.exit(1);
}
