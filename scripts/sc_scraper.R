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

# Function to wait for page load
wait_for_load <- function(session, timeout = 10) {
  session$Page$loadEventFired(wait_ = TRUE, timeout = timeout)
  Sys.sleep(3)  # Additional wait for JS rendering
}

# Function to extract document links from current page
get_document_links <- function(session) {
  # Extract all links that match download patterns
  links_js <- session$Runtime$evaluate("
    (function() {
      var links = [];

      // Pattern 1: download.ashx links (primary SC download pattern)
      document.querySelectorAll('a[href*=\"download.ashx\"]').forEach(function(a) {
        links.push(a.href);
      });

      // Pattern 2: PDF links
      document.querySelectorAll('a[href$=\".pdf\"]').forEach(function(a) {
        links.push(a.href);
      });

      // Pattern 3: Links with data-so-popup attribute (downloadable documents)
      document.querySelectorAll('a[data-so-popup]').forEach(function(a) {
        if(a.href && (a.href.includes('download') || a.href.includes('.pdf'))) {
          links.push(a.href);
        }
      });

      return [...new Set(links)];  // Remove duplicates
    })()
  ")$result$value

  if(is.null(links_js)) return(character(0))
  unique(unlist(links_js))
}

# Function to get all internal page links for deeper crawling
get_page_links <- function(session, base_path) {
  links_js <- session$Runtime$evaluate(sprintf("
    (function() {
      var links = [];
      document.querySelectorAll('a').forEach(function(a) {
        if(a.href && a.href.includes('%s') && !a.href.includes('#')) {
          links.push(a.href);
        }
      });
      return [...new Set(links)];
    })()
  ", base_path))$result$value

  if(is.null(links_js)) return(character(0))
  unique(unlist(links_js))
}

# Function to handle pagination
handle_pagination <- function(session, max_pages = 20) {
  all_links <- c()
  page <- 1

  while(page <= max_pages) {
    Sys.sleep(2)
    page_links <- get_document_links(session)
    all_links <- c(all_links, page_links)
    cat("  Page", page, ":", length(page_links), "links found\n")

    # Try to click next page
    has_next <- session$Runtime$evaluate("
      (function() {
        // Look for pagination next button
        var nextBtn = document.querySelector('.pagination .next a, .xpgn-next, a[rel=\"next\"]');
        if(nextBtn && !nextBtn.classList.contains('disabled')) {
          nextBtn.click();
          return true;
        }

        // Also try numbered pagination
        var currentPage = document.querySelector('.pagination .active, .xpgn-current');
        if(currentPage) {
          var nextPage = currentPage.nextElementSibling;
          if(nextPage && nextPage.querySelector('a')) {
            nextPage.querySelector('a').click();
            return true;
          }
        }

        return false;
      })()
    ")$result$value

    if(!isTRUE(has_next)) break

    page <- page + 1
    Sys.sleep(3)  # Wait for new page to load
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
  b$Page$navigate(full_url)
  wait_for_load(b)

  # Get links from main page with pagination
  section_links <- handle_pagination(b)

  # Also get internal page links for deeper crawling
  page_links <- get_page_links(b, target$url)

  # Crawl internal pages (up to 50 per section)
  page_count <- 0
  for(page_link in page_links) {
    if(page_count >= 50) break
    if(page_link %in% visited_pages) next

    visited_pages <- c(visited_pages, page_link)
    page_count <- page_count + 1

    tryCatch({
      cat("  Visiting:", page_link, "\n")
      b$Page$navigate(page_link)
      wait_for_load(b)

      page_doc_links <- get_document_links(b)
      section_links <- c(section_links, page_doc_links)

      if(length(page_doc_links) > 0) {
        cat("    Found", length(page_doc_links), "documents\n")
      }
    }, error = function(e) {
      cat("    Error:", conditionMessage(e), "\n")
    })
  }

  all_links <- c(all_links, section_links)
  cat("Section total:", length(section_links), "links\n")
}

# Close first browser session
b$close()

# --- Final clean-up ---
all_links <- unique(all_links)
all_links <- all_links[all_links != ""]
all_links <- all_links[!is.na(all_links)]

cat("\n=== LINK EXTRACTION COMPLETE ===\n")
cat("Total unique document links found:", length(all_links), "\n")

# Save links to file for Go to read
writeLines(all_links, links_file)
cat("Links saved to:", links_file, "\n")

# --- Download PDFs ---
cat("\nStarting PDF downloads...\n")
b2 <- ChromoteSession$new()

b2$Browser$setDownloadBehavior(
  behavior = "allow",
  downloadPath = normalizePath(output_dir)
)

success_count <- 0
fail_count <- 0

for (url in all_links) {
  tryCatch({
    # For download.ashx links, navigate directly
    if(str_detect(url, "download.ashx")) {
      b2$Page$navigate(url)
      Sys.sleep(5)
    } else {
      # For other links, trigger download
      b2$Runtime$evaluate(sprintf("
        var a = document.createElement('a');
        a.href = '%s';
        a.setAttribute('download', '');
        document.body.appendChild(a);
        a.click();
        a.remove();
      ", url))
      Sys.sleep(5)
    }

    cat("Downloaded:", basename(url), "\n")
    success_count <- success_count + 1

  }, error = function(e) {
    cat("Failed:", url, "-", conditionMessage(e), "\n")
    fail_count <- fail_count + 1
  })
}

# Close second browser session
b2$close()

cat("\n=== SCRAPE COMPLETE ===\n")
cat("Total links:", length(all_links), "\n")
cat("Downloaded:", success_count, "\n")
cat("Failed:", fail_count, "\n")
cat("Output directory:", normalizePath(output_dir), "\n")
