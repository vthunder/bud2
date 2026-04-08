#!/usr/bin/env node
/**
 * Auto-Index Generator for Knowledge Base
 *
 * Scans markdown files in a directory and generates index.md files
 * that list all documents with their first heading as description.
 *
 * Usage: node generate-index.js [directory]
 */

const fs = require('fs');
const path = require('path');

// Directories to skip
const SKIP_DIRS = new Set(['.git', 'node_modules', 'scripts', 'system/system', 'system/queues']);
const SKIP_FILES = new Set(['index.md', 'INDEX.md', 'README.md']);

/**
 * Extract the first H1 heading from a markdown file
 */
function extractFirstHeading(filePath) {
  try {
    const content = fs.readFileSync(filePath, 'utf8');
    const lines = content.split('\n');

    // Look for first # heading (H1)
    for (const line of lines) {
      const trimmed = line.trim();
      if (trimmed.startsWith('# ')) {
        return trimmed.slice(2).trim();
      }
    }

    // Fallback: use filename without extension
    return path.basename(filePath, '.md');
  } catch (err) {
    console.error(`Error reading ${filePath}:`, err.message);
    return path.basename(filePath, '.md');
  }
}

/**
 * Check if path should be skipped
 */
function shouldSkip(relativePath) {
  const parts = relativePath.split(path.sep);
  return parts.some(part => SKIP_DIRS.has(part));
}

/**
 * Recursively scan directory for markdown files
 */
function scanDirectory(dirPath, baseDir) {
  const entries = fs.readdirSync(dirPath, { withFileTypes: true });
  const files = [];
  const subdirs = [];

  for (const entry of entries) {
    const fullPath = path.join(dirPath, entry.name);
    const relativePath = path.relative(baseDir, fullPath);

    if (shouldSkip(relativePath)) continue;

    if (entry.isDirectory()) {
      subdirs.push({ name: entry.name, path: fullPath });
    } else if (entry.isFile() && entry.name.endsWith('.md') && !SKIP_FILES.has(entry.name)) {
      const heading = extractFirstHeading(fullPath);
      files.push({
        name: entry.name,
        path: fullPath,
        heading,
        relativePath
      });
    }
  }

  return { files, subdirs };
}

/**
 * Generate index.md content for a directory
 */
function generateIndexContent(dirPath, baseDir) {
  const relativePath = path.relative(baseDir, dirPath);
  const dirName = relativePath || 'Knowledge Base';

  const { files, subdirs } = scanDirectory(dirPath, baseDir);

  if (files.length === 0 && subdirs.length === 0) {
    return null; // No content to index
  }

  let content = `# ${dirName} Index\n\n`;
  content += `*Auto-generated on ${new Date().toISOString().split('T')[0]}*\n\n`;

  // List subdirectories
  if (subdirs.length > 0) {
    content += `## Subdirectories\n\n`;
    for (const subdir of subdirs.sort((a, b) => a.name.localeCompare(b.name))) {
      const { files: subFiles } = scanDirectory(subdir.path, baseDir);
      content += `- **${subdir.name}/** (${subFiles.length} files)\n`;
    }
    content += '\n';
  }

  // List files with headings
  if (files.length > 0) {
    content += `## Documents (${files.length})\n\n`;

    // Group by category based on filename patterns
    const categories = {
      'System Guides': [],
      'Research Notes': [],
      'Project Logs': [],
      'Other': []
    };

    for (const file of files) {
      if (file.relativePath.includes('system/guides')) {
        categories['System Guides'].push(file);
      } else if (file.name.includes('research') || file.name.includes('analysis')) {
        categories['Research Notes'].push(file);
      } else if (file.relativePath.includes('projects/')) {
        categories['Project Logs'].push(file);
      } else {
        categories['Other'].push(file);
      }
    }

    // Output by category
    for (const [category, categoryFiles] of Object.entries(categories)) {
      if (categoryFiles.length === 0) continue;

      content += `### ${category}\n\n`;
      for (const file of categoryFiles.sort((a, b) => a.name.localeCompare(b.name))) {
        content += `- **${file.name}** - ${file.heading}\n`;
      }
      content += '\n';
    }
  }

  return content;
}

/**
 * Main function
 */
function main() {
  const targetDir = process.argv[2] || process.cwd();

  if (!fs.existsSync(targetDir)) {
    console.error(`Directory not found: ${targetDir}`);
    process.exit(1);
  }

  console.log(`Generating index for: ${targetDir}`);

  // Generate index for root directory
  const indexContent = generateIndexContent(targetDir, targetDir);

  if (indexContent) {
    const indexPath = path.join(targetDir, 'index.md');
    fs.writeFileSync(indexPath, indexContent);
    console.log(`✓ Created ${indexPath}`);
  } else {
    console.log('No markdown files found to index');
  }

  // Recursively generate for subdirectories
  const { subdirs } = scanDirectory(targetDir, targetDir);

  for (const subdir of subdirs) {
    const subIndexContent = generateIndexContent(subdir.path, targetDir);
    if (subIndexContent) {
      const subIndexPath = path.join(subdir.path, 'index.md');
      fs.writeFileSync(subIndexPath, subIndexContent);
      console.log(`✓ Created ${subIndexPath}`);
    }
  }

  console.log('\n✅ Index generation complete');
}

main();
