/**
 * Ali MDM - Media Player Types
 * Defines types for the Media Player display mode (videos, images, playlists)
 */

export type MediaItemType = 'video' | 'image';

export interface MediaItem {
  id: string;
  url: string;
  type: MediaItemType;
  title?: string;
  duration?: number; // Display duration in seconds (for images; videos use their own duration)
  isLocal?: boolean; // True if the file is from device storage (file:// URI)
  fileName?: string; // Original filename for local files
}

export type MediaFitMode = 'contain' | 'cover' | 'fill';

export interface MediaPlayerSettings {
  items: MediaItem[];
  autoPlay: boolean;
  loop: boolean;
  shuffle: boolean;
  imageDuration: number; // Default display duration for images (seconds)
  showControls: boolean;
  fitMode: MediaFitMode;
  backgroundColor: string;
  transitionEnabled: boolean;
  transitionDuration: number; // ms
  muteVideo: boolean;
}

export const DEFAULT_MEDIA_PLAYER_SETTINGS: MediaPlayerSettings = {
  items: [],
  autoPlay: true,
  loop: true,
  shuffle: false,
  imageDuration: 10,
  showControls: false,
  fitMode: 'contain',
  backgroundColor: '#000000',
  transitionEnabled: true,
  transitionDuration: 500,
  muteVideo: false,
};

/**
 * Generate a unique ID for a media item
 */
export const generateMediaItemId = (): string => {
  return `media_${Date.now()}_${Math.random().toString(36).substring(2, 9)}`;
};

/**
 * Detect media type from URL extension
 */
export const detectMediaType = (url: string): MediaItemType => {
  const lower = url.toLowerCase().split('?')[0].split('#')[0];
  const videoExtensions = ['.mp4', '.webm', '.ogg', '.ogv', '.m3u8', '.mov', '.avi', '.mkv'];
  for (const ext of videoExtensions) {
    if (lower.endsWith(ext)) return 'video';
  }
  return 'image';
};

/**
 * Check if a media URL is a local file (file:// URI)
 */
export const isLocalMedia = (url: string): boolean => {
  return url.startsWith('file://');
};

/**
 * Get a display-friendly name for a media URL
 */
export const getMediaDisplayName = (item: MediaItem): string => {
  if (item.fileName) return item.fileName;
  if (item.title) return item.title;
  // Extract filename from URL
  try {
    const path = item.url.split('?')[0].split('#')[0];
    const segments = path.split('/');
    return segments[segments.length - 1] || item.url;
  } catch {
    return item.url;
  }
};
