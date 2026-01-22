import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { chatApi, parseApiError } from '@/services/api';
import { useChatStore } from '@/stores/chatStore';
import type { Message, ChatRequest } from '@/types';
import { v4 as uuidv4 } from './utils';

// Query keys
export const chatKeys = {
  all: ['chat'] as const,
  conversations: () => [...chatKeys.all, 'conversations'] as const,
  conversation: (id: string) => [...chatKeys.conversations(), id] as const,
};

export function useSendMessage() {
  const queryClient = useQueryClient();
  const {
    addMessage,
    updateMessage,
    setLoading,
    setError,
    conversationId,
    setConversationId,
  } = useChatStore();

  return useMutation({
    mutationFn: async (content: string) => {
      const request: ChatRequest = {
        message: content,
        conversation_id: conversationId || undefined,
      };
      return chatApi.sendMessage(request);
    },

    onMutate: async (content: string) => {
      // Optimistically add user message
      const userMessage: Message = {
        id: uuidv4(),
        role: 'user',
        content,
        timestamp: new Date(),
      };
      addMessage(userMessage);

      // Add loading message for assistant
      const loadingMessage: Message = {
        id: uuidv4(),
        role: 'assistant',
        content: '',
        timestamp: new Date(),
        isLoading: true,
      };
      addMessage(loadingMessage);
      setLoading(true);
      setError(null);

      return { loadingMessageId: loadingMessage.id };
    },

    onSuccess: (data, _content, context) => {
      // Update the loading message with the actual response
      if (context?.loadingMessageId) {
        updateMessage(context.loadingMessageId, {
          content: data.answer,
          citations: data.citations,
          isLoading: false,
        });
      }

      // Set conversation ID if this is a new conversation
      if (data.conversation_id && !conversationId) {
        setConversationId(data.conversation_id);
      }

      // Invalidate conversations list
      queryClient.invalidateQueries({ queryKey: chatKeys.conversations() });
    },

    onError: (error, _content, context) => {
      // Remove the loading message on error
      if (context?.loadingMessageId) {
        updateMessage(context.loadingMessageId, {
          content: 'Sorry, there was an error processing your request. Please try again.',
          isLoading: false,
        });
      }

      const apiError = parseApiError(error);
      setError(apiError.message);
    },

    onSettled: () => {
      setLoading(false);
    },
  });
}

export function useConversations() {
  return useQuery({
    queryKey: chatKeys.conversations(),
    queryFn: chatApi.listConversations,
    staleTime: 1000 * 60 * 5, // 5 minutes
  });
}

export function useConversation(id: string | null) {
  const { setMessages, setConversationId } = useChatStore();

  return useQuery({
    queryKey: chatKeys.conversation(id || ''),
    queryFn: async () => {
      if (!id) throw new Error('No conversation ID');
      const conversation = await chatApi.getConversation(id);

      // Map backend message format to frontend format
      const messages: Message[] = (conversation.messages || []).map((msg: any) => ({
        id: msg.id,
        role: msg.role as 'user' | 'assistant',
        content: msg.content,
        citations: msg.citations,
        timestamp: new Date(msg.created_at),
        isLoading: false,
      }));

      setMessages(messages);
      setConversationId(id);
      return conversation;
    },
    enabled: !!id,
  });
}

export function useDeleteConversation() {
  const queryClient = useQueryClient();
  const { conversationId, clearChat } = useChatStore();

  return useMutation({
    mutationFn: chatApi.deleteConversation,

    onSuccess: (_data, deletedId) => {
      // Clear chat if deleting the current conversation
      if (deletedId === conversationId) {
        clearChat();
      }

      // Invalidate conversations list
      queryClient.invalidateQueries({ queryKey: chatKeys.conversations() });
    },
  });
}
