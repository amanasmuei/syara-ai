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

# Set realistic user agent
b$Network$setUserAgentOverride(
  userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

# Function to get page HTML and parse with rvest
get_page_html <- function(session, url, wait_time = 10) {
  cat("  Fetching:", url, "\n")

  session$Page$navigate(url)
  tryCatch({
    session$Page$loadEventFired(wait_ = TRUE, timeout = 30)
  }, error = function(e) {
    cat("    Load timeout, continuing...\n")
  })
  Sys.sleep(wait_time)

  # Get HTML content
  html_result <- session$Runtime$evaluate("document.documentElement.outerHTML")
  html_content <- html_result$result$value

  if(is.null(html_content) || nchar(html_content) < 1000) {
    cat("    Warning: Page content may be incomplete\n")
    return(NULL)
  }

  read_html(html_content)
}

# Function to extract download links from parsed HTML
extract_download_links <- function(doc) {
  if(is.null(doc)) return(character(0))

  links <- doc %>% html_nodes("a") %>% html_attr("href")
  links <- links[!is.na(links)]

  # Filter for download patterns
  download_links <- links[
    grepl("download\\.ashx", links, ignore.case = TRUE) |
    grepl("\\.pdf$", links, ignore.case = TRUE) |
    grepl("/api/documentms/", links, ignore.case = TRUE)
  ]

  # Normalize URLs
  download_links <- sapply(download_links, function(link) {
    if(grepl("^https?://", link)) {
      link
    } else if(startsWith(link, "/")) {
      paste0(base_url, link)
    } else {
      paste0(base_url, "/", link)
    }
  })

  unique(unname(download_links))
}

# Function to extract subpage links for deeper crawling
extract_subpage_links <- function(doc, base_path) {
  if(is.null(doc)) return(character(0))

  links <- doc %>% html_nodes("a") %>% html_attr("href")
  links <- links[!is.na(links)]

  # Remove base_url prefix for matching if present
  base_path_clean <- gsub("^/", "", base_path)

  # Filter for subpage links (match both absolute and relative paths)
  subpage_links <- links[
    grepl(base_path, links, ignore.case = TRUE) |
    grepl(base_path_clean, links, ignore.case = TRUE)
  ]
  subpage_links <- subpage_links[!grepl("#", subpage_links)]
  subpage_links <- subpage_links[!grepl("download\\.ashx", subpage_links)]
  subpage_links <- subpage_links[!grepl("\\.pdf$", subpage_links, ignore.case = TRUE)]

  # Normalize URLs
  subpage_links <- sapply(subpage_links, function(link) {
    if(grepl("^https?://", link)) {
      link
    } else if(startsWith(link, "/")) {
      paste0(base_url, link)
    } else {
      paste0(base_url, "/", link)
    }
  })

  # Remove duplicates and the main page itself
  unique(unname(subpage_links))
}

all_links <- c()
visited_pages <- c()

# --- Crawl each target section ---
for(target_name in names(target_pages)) {
  target <- target_pages[[target_name]]
  cat("\n=== Crawling", target$name, "===\n")

  full_url <- paste0(base_url, target$url)

  # Get main page
  doc <- get_page_html(b, full_url, wait_time = 10)
  visited_pages <- c(visited_pages, full_url)

  if(!is.null(doc)) {
    # Extract download links from main page
    main_links <- extract_download_links(doc)
    cat("  Found", length(main_links), "download links on main page\n")
    all_links <- c(all_links, main_links)

    # Get subpages to crawl
    subpages <- extract_subpage_links(doc, target$url)
    subpages <- setdiff(subpages, visited_pages)
    cat("  Found", length(subpages), "subpages to crawl\n")

    # Crawl subpages (limit to 50)
    subpage_count <- 0
    for(subpage in subpages) {
      if(subpage_count >= 50) break
      if(subpage %in% visited_pages) next

      visited_pages <- c(visited_pages, subpage)
      subpage_count <- subpage_count + 1

      tryCatch({
        sub_doc <- get_page_html(b, subpage, wait_time = 5)

        if(!is.null(sub_doc)) {
          sub_links <- extract_download_links(sub_doc)
          new_links <- setdiff(sub_links, all_links)

          if(length(new_links) > 0) {
            cat("    [", subpage_count, "] Found", length(new_links), "new links in", basename(subpage), "\n")
            all_links <- c(all_links, new_links)
          }
        }
      }, error = function(e) {
        cat("    Error crawling", basename(subpage), ":", conditionMessage(e), "\n")
      })
    }
  }

  cat("  Section total:", length(unique(all_links)), "unique links so far\n")
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
  for(link in head(all_links, 10)) {
    cat("  -", link, "\n")
  }
}

# Save links to file for Go to read
writeLines(all_links, links_file)
cat("\nLinks saved to:", links_file, "\n")

# --- Download PDFs ---
if(length(all_links) > 0) {
  cat("\nStarting PDF downloads...\n")

  success_count <- 0
  fail_count <- 0

  for (i in seq_along(all_links)) {
    url <- all_links[i]
    tryCatch({
      # Generate filename from URL
      if(grepl("download\\.ashx\\?id=", url)) {
        # Extract UUID from ashx URL
        id <- sub(".*id=([^&]+).*", "\\1", url)
        filename <- paste0("sc_doc_", id, ".pdf")
      } else {
        filename <- basename(url)
        if(!grepl("\\.pdf$", filename, ignore.case = TRUE)) {
          filename <- paste0(filename, ".pdf")
        }
      }

      filepath <- file.path(output_dir, filename)

      cat("[", i, "/", length(all_links), "] Downloading:", filename, "\n")

      # Use download.file with proper headers
      download.file(
        url = url,
        destfile = filepath,
        mode = "wb",
        quiet = TRUE,
        headers = c(
          "User-Agent" = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"
        )
      )

      # Verify file was downloaded
      if(file.exists(filepath) && file.size(filepath) > 0) {
        success_count <- success_count + 1
        cat("    Saved:", filename, "(", file.size(filepath), "bytes )\n")
      } else {
        fail_count <- fail_count + 1
        cat("    Failed: Empty or missing file\n")
      }

      Sys.sleep(1)  # Rate limiting

    }, error = function(e) {
      cat("  Failed:", conditionMessage(e), "\n")
      fail_count <- fail_count + 1
    })
  }

  cat("\n=== DOWNLOAD COMPLETE ===\n")
  cat("Downloaded:", success_count, "\n")
  cat("Failed:", fail_count, "\n")
} else {
  cat("\nNo documents to download.\n")
}

cat("Output directory:", normalizePath(output_dir), "\n")
