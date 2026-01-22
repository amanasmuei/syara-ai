#!/usr/bin/env Rscript
# Securities Commission Malaysia Scraper
# This script scrapes PDF links from SC Malaysia website and downloads them

library(chromote)
library(rvest)
library(stringr)

# Parse command line arguments
args <- commandArgs(trailingOnly = TRUE)
output_dir <- if(length(args) > 0) args[1] else "sc_files"
links_file <- if(length(args) > 1) args[2] else file.path(output_dir, "links.txt")
category <- if(length(args) > 2) args[3] else "all"

cat("Output directory:", output_dir, "\n")
cat("Links file:", links_file, "\n")
cat("Category:", category, "\n")

base_url <- "https://www.sc.com.my"

# Define target pages by category
target_pages <- list(
  guidelines = list(
    url = "/regulation/guidelines",
    name = "Guidelines"
  ),
  acts = list(
    url = "/regulation/acts",
    name = "Acts"
  ),
  icm = list(
    url = "/development/islamic-capital-market",
    name = "Islamic Capital Market"
  ),
  sac = list(
    url = "/development/islamic-capital-market/shariah-advisory-council",
    name = "Shariah Advisory Council"
  )
)

# Filter by category if specified
if(category != "all" && category %in% names(target_pages)) {
  target_pages <- target_pages[category]
}

# Create output directory
dir.create(output_dir, showWarnings = FALSE, recursive = TRUE)

# --- Start Chromote session ---
cat("Starting browser session...\n")
b <- ChromoteSession$new()

# Function to wait for page load with extended JS wait
wait_for_load <- function(session, timeout = 30) {
  tryCatch({
    session$Page$loadEventFired(wait_ = TRUE, timeout = timeout)
  }, error = function(e) {
    cat("  Load event timeout, continuing...\n")
  })
  Sys.sleep(5)  # Wait for JS rendering

  # Scroll to trigger lazy loading
  session$Runtime$evaluate("window.scrollTo(0, document.body.scrollHeight/2);")
  Sys.sleep(2)
  session$Runtime$evaluate("window.scrollTo(0, document.body.scrollHeight);")
  Sys.sleep(2)
  session$Runtime$evaluate("window.scrollTo(0, 0);")
  Sys.sleep(1)
}

# Function to debug page content
debug_page <- function(session) {
  # Get page title
  title <- session$Runtime$evaluate("document.title")$result$value
  cat("  Page title:", title, "\n")

  # Count all links
  link_count <- session$Runtime$evaluate("document.querySelectorAll('a').length")$result$value
  cat("  Total links on page:", link_count, "\n")

  # Check for specific elements
  has_pag_list <- session$Runtime$evaluate("document.querySelector('.pag-list-01') !== null")$result$value
  cat("  Has .pag-list-01:", has_pag_list, "\n")

  # Get some sample hrefs
  sample_hrefs <- session$Runtime$evaluate("
    Array.from(document.querySelectorAll('a')).slice(0, 20).map(a => a.href).filter(h => h.includes('download') || h.includes('.pdf'))
  ")$result$value

  if(length(sample_hrefs) > 0) {
    cat("  Sample download links found:\n")
    for(href in sample_hrefs) {
      cat("    -", href, "\n")
    }
  }
}

# Function to extract document links from current page
get_document_links <- function(session) {
  # Extract all links that match download patterns
  links_js <- session$Runtime$evaluate("
    (function() {
      var links = [];

      // Get all anchor elements
      var anchors = document.querySelectorAll('a');

      anchors.forEach(function(a) {
        var href = a.href || '';

        // Pattern 1: download.ashx links (primary SC download pattern)
        if(href.includes('download.ashx')) {
          links.push(href);
        }

        // Pattern 2: PDF links
        if(href.toLowerCase().endsWith('.pdf')) {
          links.push(href);
        }

        // Pattern 3: documentms API
        if(href.includes('/api/documentms/')) {
          links.push(href);
        }
      });

      // Also check onclick handlers for download links
      document.querySelectorAll('[onclick*=\"download\"]').forEach(function(el) {
        var onclick = el.getAttribute('onclick') || '';
        var match = onclick.match(/['\"]([^'\"]*download[^'\"]*)['\"]/) ||
                    onclick.match(/['\"]([^'\"]*\\.pdf)['\"]/) ||
                    onclick.match(/window\\.open\\(['\"]([^'\"]+)['\"]\\)/);
        if(match && match[1]) {
          var url = match[1];
          if(!url.startsWith('http')) {
            url = window.location.origin + (url.startsWith('/') ? '' : '/') + url;
          }
          links.push(url);
        }
      });

      return [...new Set(links)];
    })()
  ")$result$value

  if(is.null(links_js) || length(links_js) == 0) return(character(0))
  unique(unlist(links_js))
}

# Function to get all internal page links for deeper crawling
get_page_links <- function(session, base_path) {
  links_js <- session$Runtime$evaluate(sprintf("
    (function() {
      var links = [];
      var base = '%s';
      document.querySelectorAll('a').forEach(function(a) {
        var href = a.href || '';
        if(href.includes(base) && !href.includes('#') && href !== window.location.href) {
          links.push(href);
        }
      });
      return [...new Set(links)];
    })()
  ", base_path))$result$value

  if(is.null(links_js) || length(links_js) == 0) return(character(0))
  unique(unlist(links_js))
}

# Function to handle pagination by clicking through pages
handle_pagination <- function(session, max_pages = 10) {
  all_links <- c()
  page <- 1

  while(page <= max_pages) {
    Sys.sleep(3)  # Wait for content to load

    # Debug current page
    cat("  Scanning page", page, "...\n")

    page_links <- get_document_links(session)
    new_links <- setdiff(page_links, all_links)
    all_links <- c(all_links, page_links)

    cat("    Found", length(new_links), "new links (total:", length(unique(all_links)), ")\n")

    # Try to find and click next page
    has_next <- session$Runtime$evaluate("
      (function() {
        // Method 1: Look for pagination with numbers
        var paginationLinks = document.querySelectorAll('.pagination a, .xpgn a, [class*=\"paginate\"] a');
        var currentPageNum = null;
        var nextLink = null;

        // Find current page number
        var activeEl = document.querySelector('.pagination .active, .xpgn .active, [class*=\"current\"]');
        if(activeEl) {
          currentPageNum = parseInt(activeEl.textContent.trim());
        }

        // Look for next page number
        if(currentPageNum) {
          for(var i = 0; i < paginationLinks.length; i++) {
            var linkNum = parseInt(paginationLinks[i].textContent.trim());
            if(linkNum === currentPageNum + 1) {
              paginationLinks[i].click();
              return true;
            }
          }
        }

        // Method 2: Look for 'Next' button
        var nextBtns = document.querySelectorAll('a.next, .next a, [class*=\"next\"] a, a[rel=\"next\"]');
        for(var i = 0; i < nextBtns.length; i++) {
          if(!nextBtns[i].classList.contains('disabled') && nextBtns[i].offsetParent !== null) {
            nextBtns[i].click();
            return true;
          }
        }

        // Method 3: Look for '>' or '>>' buttons
        var allLinks = document.querySelectorAll('.pagination a, .xpgn a');
        for(var i = 0; i < allLinks.length; i++) {
          var text = allLinks[i].textContent.trim();
          if(text === '>' || text === '>>' || text === 'Next') {
            allLinks[i].click();
            return true;
          }
        }

        return false;
      })()
    ")$result$value

    if(!isTRUE(has_next)) {
      cat("    No more pages found\n")
      break
    }

    page <- page + 1
    Sys.sleep(3)  # Wait for page change
  }

  unique(all_links)
}

all_links <- c()
visited_pages <- c()

# --- Crawl each target section ---
for(target_name in names(target_pages)) {
  target <- target_pages[[target_name]]
  cat("\n=== Crawling", target$name, "===\n")

  full_url <- paste0(base_url, target$url)
  cat("URL:", full_url, "\n")

  b$Page$navigate(full_url)
  wait_for_load(b)

  # Debug the page
  debug_page(b)

  # Get links from main page with pagination
  section_links <- handle_pagination(b)

  # Also get internal page links for deeper crawling
  internal_links <- get_page_links(b, target$url)
  cat("Found", length(internal_links), "internal pages to crawl\n")

  # Crawl internal pages (up to 30 per section)
  page_count <- 0
  for(page_link in internal_links) {
    if(page_count >= 30) break
    if(page_link %in% visited_pages) next
    if(page_link == full_url) next

    visited_pages <- c(visited_pages, page_link)
    page_count <- page_count + 1

    tryCatch({
      cat("  [", page_count, "/", min(30, length(internal_links)), "] Visiting:", basename(page_link), "\n")
      b$Page$navigate(page_link)
      wait_for_load(b)

      page_doc_links <- get_document_links(b)
      new_links <- setdiff(page_doc_links, section_links)
      section_links <- c(section_links, page_doc_links)

      if(length(new_links) > 0) {
        cat("      Found", length(new_links), "new documents\n")
      }
    }, error = function(e) {
      cat("      Error:", conditionMessage(e), "\n")
    })
  }

  all_links <- c(all_links, section_links)
  cat("Section total:", length(unique(section_links)), "unique links\n")
}

# Close first browser session
b$close()

# --- Final clean-up ---
all_links <- unique(all_links)
all_links <- all_links[all_links != ""]
all_links <- all_links[!is.na(all_links)]

cat("\n=== LINK EXTRACTION COMPLETE ===\n")
cat("Total unique document links found:", length(all_links), "\n")

if(length(all_links) > 0) {
  cat("\nSample links:\n")
  for(link in head(all_links, 5)) {
    cat("  -", link, "\n")
  }
}

# Save links to file for Go to read
writeLines(all_links, links_file)
cat("\nLinks saved to:", links_file, "\n")

# --- Download PDFs ---
if(length(all_links) > 0) {
  cat("\nStarting PDF downloads...\n")
  b2 <- ChromoteSession$new()

  b2$Browser$setDownloadBehavior(
    behavior = "allow",
    downloadPath = normalizePath(output_dir)
  )

  success_count <- 0
  fail_count <- 0

  for (i in seq_along(all_links)) {
    url <- all_links[i]
    tryCatch({
      cat("[", i, "/", length(all_links), "] Downloading:", basename(url), "\n")

      # Navigate to trigger download
      b2$Page$navigate(url)
      Sys.sleep(5)

      success_count <- success_count + 1

    }, error = function(e) {
      cat("  Failed:", conditionMessage(e), "\n")
      fail_count <- fail_count + 1
    })
  }

  # Close second browser session
  b2$close()

  cat("\n=== DOWNLOAD COMPLETE ===\n")
  cat("Downloaded:", success_count, "\n")
  cat("Failed:", fail_count, "\n")
} else {
  cat("\nNo documents to download.\n")
}

cat("Output directory:", normalizePath(output_dir), "\n")
