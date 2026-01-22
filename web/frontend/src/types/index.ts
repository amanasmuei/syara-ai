// Core Types for SyaRA - AI Frontend

export interface Message {
  id: string;
  role: 'user' | 'assistant';
  content: string;
  citations?: Citation[];
  timestamp: Date;
  isLoading?: boolean;
}

export interface Citation {
  id: string;
  index: number;
  content_snippet: string;
  source: SourceInfo;
  visual: VisualCitation;
  download: DownloadLinks;
  relevance: number;
}

export interface SourceInfo {
  name: string;
  type: 'bnm' | 'aaoifi' | 'other';
  document_id: string;
  page_number: number;
  section?: string;
  url?: string;
}

export interface VisualCitation {
  thumbnail_url: string;
  highlighted_image_url: string;
  original_image_url: string;
  bounding_box?: BoundingBox;
}

export interface BoundingBox {
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface DownloadLinks {
  full_pdf: string;
  single_page: string;
}

export interface Conversation {
  id: string;
  title: string;
  messages: Message[];
  created_at: Date;
  updated_at: Date;
}

export interface ChatRequest {
  message: string;
  conversation_id?: string;
}

export interface ChatResponse {
  answer: string;
  citations: Citation[];
  metadata: ResponseMetadata;
  conversation_id: string;
}

export interface ResponseMetadata {
  model: string;
  processing_time_ms: number;
  sources_consulted: number;
}

export interface UpdateNotification {
  type: 'document_updated' | 'cache_invalidated' | 'system';
  source?: string;
  message: string;
  timestamp: Date;
}

export interface WebSocketMessage {
  type: string;
  payload: unknown;
}

// UI State Types
export interface ChatState {
  messages: Message[];
  isLoading: boolean;
  error: string | null;
  conversationId: string | null;
  selectedCitation: Citation | null;
}

export interface UIState {
  isSidebarOpen: boolean;
  isCitationPanelOpen: boolean;
  theme: 'light' | 'dark';
}

// Settings Types
export interface CrawlerSource {
  id: string;
  name: string;
  type: 'bnm' | 'sc' | 'iifa' | 'fatwa';
  baseUrl: string;
  description: string;
  status: 'active' | 'inactive' | 'error';
  lastCrawled?: Date;
  documentCount: number;
}

export interface UploadedFile {
  id: string;
  filename: string;
  fileType: string;
  fileSize: number;
  uploadedAt: Date;
  status: 'processing' | 'indexed' | 'failed';
  pageCount?: number;
  source: string;
}

export interface CrawlerJob {
  id: string;
  crawlerType: string;
  status: 'pending' | 'running' | 'completed' | 'failed';
  startedAt?: Date;
  completedAt?: Date;
  documentsProcessed: number;
  documentsTotal: number;
  errorMessage?: string;
}

export type SettingsTab = 'crawlers' | 'files' | 'jobs';
