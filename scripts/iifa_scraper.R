#!/usr/bin/env Rscript
# IIFA (International Islamic Fiqh Academy) Resolutions Scraper
# This script scrapes resolution links from IIFA website and downloads PDFs

library(chromote)
library(rvest)
library(stringr)

# Parse command line arguments
args <- commandArgs(trailingOnly = TRUE)
output_dir <- if(length(args) > 0) args[1] else "iifa_files"
links_file <- if(length(args) > 1) args[2] else file.path(output_dir, "links.txt")

cat("Output directory:", output_dir, "\n")
cat("Links file:", links_file, "\n")

base_url <- "https://iifa-aifi.org"
resolutions_url <- paste0(base_url, "/en/resolutions")

# Create output directory
dir.create(output_dir, showWarnings = FALSE, recursive = TRUE)

# --- Start Chromote session ---
cat("Starting browser session...\n")
b <- ChromoteSession$new()

# Function to wait for page load
wait_for_load <- function(session, timeout = 15) {
  session$Page$loadEventFired(wait_ = TRUE, timeout = timeout)
  Sys.sleep(3)  # Additional wait for JS rendering
}

# Function to extract resolution links from current page
get_resolution_links <- function(session) {
  links_js <- session$Runtime$evaluate("
    (function() {
      var links = [];

      // Pattern 1: Resolution post links from grid
      document.querySelectorAll('.fusion-post-grid a, .fusion-blog-shortcode a').forEach(function(a) {
        if(a.href && a.href.includes('/en/') && a.href.match(/\\/\\d+\\.html$/)) {
          links.push(a.href);
        }
      });

      // Pattern 2: Any links with resolution number pattern
      document.querySelectorAll('a').forEach(function(a) {
        if(a.href && a.href.match(/iifa-aifi\\.org\\/en\\/\\d+\\.html/)) {
          links.push(a.href);
        }
      });

      // Pattern 3: Resolution titles in headings
      document.querySelectorAll('h2 a, h3 a, h4 a').forEach(function(a) {
        if(a.href && a.href.includes('iifa-aifi.org')) {
          links.push(a.href);
        }
      });

      return [...new Set(links)];  // Remove duplicates
    })()
  ")$result$value

  if(is.null(links_js)) return(character(0))
  unique(unlist(links_js))
}

# Function to extract PDF links from a resolution page
get_pdf_links <- function(session) {
  links_js <- session$Runtime$evaluate("
    (function() {
      var links = [];

      // PDF download links
      document.querySelectorAll('a[href$=\".pdf\"]').forEach(function(a) {
        links.push(a.href);
      });

      // Links with download attribute
      document.querySelectorAll('a[download]').forEach(function(a) {
        if(a.href) links.push(a.href);
      });

      // wp-content uploads (common WordPress PDF location)
      document.querySelectorAll('a[href*=\"wp-content/uploads\"]').forEach(function(a) {
        if(a.href.includes('.pdf')) links.push(a.href);
      });

      return [...new Set(links)];
    })()
  ")$result$value

  if(is.null(links_js)) return(character(0))
  unique(unlist(links_js))
}

# Function to check for next page
has_next_page <- function(session) {
  result <- session$Runtime$evaluate("
    (function() {
      // Check for next link in pagination
      var nextLink = document.querySelector('a.pagination-next, a[rel=\"next\"], .pagination-next a');
      if(nextLink) return nextLink.href;

      // Check for numbered pagination
      var pagination = document.querySelector('.pagination');
      if(pagination) {
        var links = pagination.querySelectorAll('a');
        for(var i = 0; i < links.length; i++) {
          if(links[i].textContent.trim() === 'Next' || links[i].textContent.includes('>')) {
            return links[i].href;
          }
        }
      }

      // Check for load more button
      var loadMore = document.querySelector('.fusion-load-more-button');
      if(loadMore) return 'load_more';

      return null;
    })()
  ")$result$value

  result
}

# Function to click load more
click_load_more <- function(session) {
  session$Runtime$evaluate("
    var btn = document.querySelector('.fusion-load-more-button');
    if(btn) btn.click();
  ")
  Sys.sleep(3)
}

all_resolution_links <- c()
all_pdf_links <- c()

# --- Navigate to resolutions page ---
cat("Navigating to IIFA resolutions page...\n")
b$Page$navigate(resolutions_url)
wait_for_load(b)

# --- First, try to get the comprehensive e-book PDF ---
cat("Looking for comprehensive e-book PDF...\n")
ebook_links <- get_pdf_links(b)
if(length(ebook_links) > 0) {
  cat("Found", length(ebook_links), "PDF links on main page\n")
  all_pdf_links <- c(all_pdf_links, ebook_links)
}

# --- Crawl resolution listing pages ---
page <- 1
max_pages <- 50  # Safety limit

cat("\nScraping resolution listing pages...\n")
while(page <= max_pages) {
  cat("Page", page, ":\n")

  # Get resolution links from current page
  page_links <- get_resolution_links(b)
  all_resolution_links <- c(all_resolution_links, page_links)
  cat("  Found", length(page_links), "resolution links\n")

  # Check for next page
  next_page <- has_next_page(b)

  if(is.null(next_page)) {
    cat("  No more pages found\n")
    break
  }

  if(next_page == "load_more") {
    # Click load more and continue on same page
    cat("  Clicking load more...\n")
    click_load_more(b)
    Sys.sleep(3)
  } else {
    # Navigate to next page
    cat("  Navigating to next page...\n")
    b$Page$navigate(next_page)
    wait_for_load(b)
  }

  page <- page + 1
}

all_resolution_links <- unique(all_resolution_links)
cat("\nTotal resolution pages found:", length(all_resolution_links), "\n")

# --- Crawl individual resolution pages for PDFs ---
cat("\nCrawling individual resolution pages...\n")
visited <- c()

for(i in seq_along(all_resolution_links)) {
  url <- all_resolution_links[i]
  if(url %in% visited) next
  visited <- c(visited, url)

  tryCatch({
    cat("  [", i, "/", length(all_resolution_links), "]", basename(url), "\n")
    b$Page$navigate(url)
    wait_for_load(b)

    # Get PDF links from resolution page
    pdf_links <- get_pdf_links(b)
    if(length(pdf_links) > 0) {
      cat("    Found", length(pdf_links), "PDFs\n")
      all_pdf_links <- c(all_pdf_links, pdf_links)
    }

    # Also extract page content link if present
    content_html <- b$Runtime$evaluate("
      document.querySelector('.post-content, .entry-content, article')?.innerHTML
    ")$result$value

    Sys.sleep(1)  # Rate limiting

  }, error = function(e) {
    cat("    Error:", conditionMessage(e), "\n")
  })
}

# Close first browser session
b$close()

# --- Final clean-up ---
all_pdf_links <- unique(all_pdf_links)
all_pdf_links <- all_pdf_links[all_pdf_links != ""]
all_pdf_links <- all_pdf_links[!is.na(all_pdf_links)]

# Combine resolution page links and PDF links
all_links <- unique(c(all_resolution_links, all_pdf_links))

cat("\n=== LINK EXTRACTION COMPLETE ===\n")
cat("Resolution pages:", length(all_resolution_links), "\n")
cat("PDF links:", length(all_pdf_links), "\n")
cat("Total unique links:", length(all_links), "\n")

# Save links to file for Go to read
writeLines(all_links, links_file)
cat("Links saved to:", links_file, "\n")

# Save PDF links separately
pdf_links_file <- file.path(output_dir, "pdf_links.txt")
writeLines(all_pdf_links, pdf_links_file)
cat("PDF links saved to:", pdf_links_file, "\n")

# --- Download PDFs ---
if(length(all_pdf_links) > 0) {
  cat("\nStarting PDF downloads...\n")
  b2 <- ChromoteSession$new()

  b2$Browser$setDownloadBehavior(
    behavior = "allow",
    downloadPath = normalizePath(output_dir)
  )

  success_count <- 0
  fail_count <- 0

  for (url in all_pdf_links) {
    tryCatch({
      cat("Downloading:", basename(url), "\n")

      # Navigate to PDF URL to trigger download
      b2$Page$navigate(url)
      Sys.sleep(5)

      success_count <- success_count + 1

    }, error = function(e) {
      cat("Failed:", url, "-", conditionMessage(e), "\n")
      fail_count <- fail_count + 1
    })
  }

  # Close second browser session
  b2$close()

  cat("\n=== DOWNLOAD COMPLETE ===\n")
  cat("Downloaded:", success_count, "\n")
  cat("Failed:", fail_count, "\n")
} else {
  cat("\nNo PDFs to download.\n")
}

cat("Output directory:", normalizePath(output_dir), "\n")
