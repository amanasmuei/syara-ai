import axios, { AxiosError, AxiosInstance, InternalAxiosRequestConfig, AxiosResponse } from 'axios';
import type {
  ChatRequest,
  ChatResponse,
  Conversation,
} from '@/types';

// Create axios instance
const api: AxiosInstance = axios.create({
  baseURL: import.meta.env.VITE_API_URL || '/api',
  headers: {
    'Content-Type': 'application/json',
  },
  timeout: 60000, // 60 seconds for long AI responses
});

// Request interceptor for logging in development
api.interceptors.request.use(
  (config: InternalAxiosRequestConfig) => {
    if (import.meta.env.DEV) {
      console.log(`[API Request] ${config.method?.toUpperCase()} ${config.url}`, config.data);
    }
    return config;
  },
  (error: AxiosError) => {
    console.error('[API Request Error]', error);
    return Promise.reject(error);
  }
);

// Response interceptor for error handling
api.interceptors.response.use(
  (response: AxiosResponse) => {
    if (import.meta.env.DEV) {
      console.log(`[API Response] ${response.status}`, response.data);
    }
    return response;
  },
  (error: AxiosError) => {
    if (import.meta.env.DEV) {
      console.error('[API Response Error]', error.response?.data || error.message);
    }

    // Handle specific error codes
    if (error.response) {
      const status = error.response.status;

      switch (status) {
        case 401:
          // Handle unauthorized - could redirect to login
          console.warn('Unauthorized access');
          break;
        case 403:
          console.warn('Forbidden access');
          break;
        case 404:
          console.warn('Resource not found');
          break;
        case 429:
          console.warn('Rate limited - please wait');
          break;
        case 500:
          console.error('Server error');
          break;
      }
    }

    return Promise.reject(error);
  }
);

// API Error type
export interface ApiError {
  message: string;
  code?: string;
  details?: Record<string, unknown>;
}

// Parse error response
export function parseApiError(error: unknown): ApiError {
  if (axios.isAxiosError(error)) {
    const data = error.response?.data;
    if (data && typeof data === 'object') {
      return {
        message: data.message || data.error || 'An error occurred',
        code: data.code,
        details: data.details,
      };
    }
    return {
      message: error.message || 'Network error',
    };
  }
  return {
    message: error instanceof Error ? error.message : 'Unknown error',
  };
}

// Chat API
export const chatApi = {
  sendMessage: async (req: ChatRequest): Promise<ChatResponse> => {
    const response = await api.post<ChatResponse>('/chat', req);
    return response.data;
  },

  getConversation: async (id: string): Promise<Conversation> => {
    const response = await api.get<Conversation>(`/conversations/${id}`);
    return response.data;
  },

  listConversations: async (): Promise<Conversation[]> => {
    const response = await api.get<Conversation[]>('/conversations');
    return response.data;
  },

  deleteConversation: async (id: string): Promise<void> => {
    await api.delete(`/conversations/${id}`);
  },
};

// Document API
export const documentApi = {
  getDownloadUrl: (fileId: string, type: 'original' | 'page' = 'page'): string => {
    const baseUrl = import.meta.env.VITE_API_URL || '/api';
    return `${baseUrl}/download/${fileId}?type=${type}`;
  },

  getThumbnailUrl: (fileId: string, pageNumber: number): string => {
    const baseUrl = import.meta.env.VITE_API_URL || '/api';
    return `${baseUrl}/thumbnails/${fileId}/${pageNumber}`;
  },
};

// Health check API
export const healthApi = {
  check: async (): Promise<{ status: string; version: string }> => {
    const response = await api.get<{ status: string; version: string }>('/health');
    return response.data;
  },
};

export default api;
