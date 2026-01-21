import { create } from 'zustand';
import type { Message, Citation } from '@/types';

interface ChatState {
  messages: Message[];
  isLoading: boolean;
  error: string | null;
  conversationId: string | null;
  selectedCitation: Citation | null;

  // Actions
  addMessage: (message: Message) => void;
  updateMessage: (id: string, updates: Partial<Message>) => void;
  setMessages: (messages: Message[]) => void;
  setLoading: (loading: boolean) => void;
  setError: (error: string | null) => void;
  setConversationId: (id: string | null) => void;
  setSelectedCitation: (citation: Citation | null) => void;
  clearChat: () => void;
}

export const useChatStore = create<ChatState>((set) => ({
  messages: [],
  isLoading: false,
  error: null,
  conversationId: null,
  selectedCitation: null,

  addMessage: (message) =>
    set((state) => ({
      messages: [...state.messages, message],
    })),

  updateMessage: (id, updates) =>
    set((state) => ({
      messages: state.messages.map((msg) =>
        msg.id === id ? { ...msg, ...updates } : msg
      ),
    })),

  setMessages: (messages) => set({ messages }),

  setLoading: (isLoading) => set({ isLoading }),

  setError: (error) => set({ error }),

  setConversationId: (conversationId) => set({ conversationId }),

  setSelectedCitation: (selectedCitation) => set({ selectedCitation }),

  clearChat: () =>
    set({
      messages: [],
      error: null,
      conversationId: null,
      selectedCitation: null,
    }),
}));
