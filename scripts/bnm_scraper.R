#!/usr/bin/env Rscript
# BNM Policy Documents Scraper
# This script scrapes PDF links from BNM website and downloads them

library(chromote)
library(rvest)
library(stringr)

# Parse command line arguments
args <- commandArgs(trailingOnly = TRUE)
output_dir <- if(length(args) > 0) args[1] else "bnm_files"
links_file <- if(length(args) > 1) args[2] else file.path(output_dir, "links.txt")

cat("Output directory:", output_dir, "\n")
cat("Links file:", links_file, "\n")

base_url <- "https://www.bnm.gov.my"

# Create output directory
dir.create(output_dir, showWarnings = FALSE, recursive = TRUE)

# --- Start Chromote session ---
cat("Starting browser session...\n")
b <- ChromoteSession$new()
b$Page$navigate(paste0(base_url, "/banking-islamic-banking"))
b$Page$loadEventFired(wait_ = TRUE)
Sys.sleep(5)

# --- Tick "Policy Document" checkbox ---
cat("Clicking Policy Document filter...\n")
b$Runtime$evaluate("
  var checkbox = document.querySelector('input[name=\"pos\"][value=\"Policy Document\"]');
  if(checkbox && !checkbox.checked) { checkbox.click(); }
")
Sys.sleep(5)

# --- Function to get links from table on current page ---
get_table_links <- function(session) {
  table_html <- session$Runtime$evaluate("
    document.querySelector('table#filta')?.outerHTML
  ")$result$value

  if(is.null(table_html)) return(character(0))

  parsed <- read_html(table_html)
  links <- parsed %>% html_nodes("tbody a") %>% html_attr("href")

  # Normalize relative links
  links <- sapply(links, function(x) {
    if(!str_detect(x, "^https?://")) paste0(base_url, x) else x
  })

  unique(links)
}

all_links <- c()

total_pages <- 12  # hardcoded because the site only has 12 pages

# --- Loop through all pages ---
cat("Scraping", total_pages, "pages...\n")
for(page in 1:total_pages) {
  Sys.sleep(2)  # wait for table to render
  page_links <- get_table_links(b)
  all_links <- c(all_links, page_links)
  cat("Page", page, ":", length(page_links), "links found\n")

  # Click next page if not last
  if(page < total_pages) {
    b$Runtime$evaluate(sprintf("
      var btns = Array.from(document.querySelectorAll('a.paginate_button'));
      var next = btns.find(b => b.textContent.trim() == '%d');
      if(next) { next.click(); }
    ", page + 1))
    Sys.sleep(3)  # wait for new page to render
  }
}

# Close first browser session
b$close()

# --- Final clean-up ---
all_links <- unique(all_links)
all_links <- all_links[all_links != ""]

cat("Total unique links found:", length(all_links), "\n")

# Save links to file for Go to read
writeLines(all_links, links_file)
cat("Links saved to:", links_file, "\n")

# --- Download PDFs ---
cat("Starting PDF downloads...\n")
b2 <- ChromoteSession$new()

b2$Browser$setDownloadBehavior(
  behavior = "allow",
  downloadPath = normalizePath(output_dir)
)

success_count <- 0
fail_count <- 0

for (url in all_links) {
  tryCatch({
    b2$Runtime$evaluate(sprintf("
      var a = document.createElement('a');
      a.href = '%s';
      a.setAttribute('download', '');
      document.body.appendChild(a);
      a.click();
      a.remove();
    ", url))

    Sys.sleep(5)
    cat("Downloaded:", url, "\n")
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
