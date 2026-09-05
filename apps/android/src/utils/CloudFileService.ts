/**
 * Console → device file inbox (tablet side).
 *
 * The server queues a file per device; the tablet fetches the list, downloads
 * each file, drops it into the shared "Ali MDM" folder under Downloads, and
 * reports what happened. The report is the part that matters: a teacher who
 * pushed a worksheet needs to know which tablets actually took it, and a silent
 * failure here would leave the console claiming a delivery that never landed.
 *
 * Deliberately a separate channel from commands and APK updates, so a stuck
 * command cannot hold up a worksheet, and a large download cannot delay a
 * reboot.
 */
import { NativeModules } from 'react-native';
import RNFS from 'react-native-fs';

const { InboxModule } = NativeModules;

export interface CloudCredentials {
  cloudUrl: string;
  deviceId: string;
  apiKey: string;
}

interface PendingFile {
  name: string;
  sha256: string;
  size: number;
  content_type: string;
  /** Folder under the inbox to place this in, e.g. "Juz30/Surah-078". */
  rel_path?: string;
  /** Name to use inside that folder. The catalogue key carries the folders too,
   *  so a pupil would otherwise see "Juz30 - Surah-078 - track01.mp3". */
  file_name?: string;
  download_url: string;
}

export interface InboxEntry {
  name: string;
  size: number;
  mime_type: string;
  modified_at: number;
  /** Folder within the inbox, "" at the top. Two folders can hold the same file
   *  name, so this is what tells them apart in the console and on delete. */
  rel_path?: string;
}

/** Matches the server's delivery states. */
const STATUS_DONE = 'done';
const STATUS_FAILED = 'failed';

/** How many server batches one poll will work through before giving the rest to
    the next check-in. Twenty-five files a batch, so this covers a folder of a
    thousand. */
const MAX_BATCHES_PER_POLL = 40;

class CloudFileServiceImpl {
  /** Guards against a slow download overlapping the next heartbeat's poll. */
  private polling = false;

  /**
   * Fetch and store every file waiting for this device.
   *
   * The server marks a file "sent" when it hands it over, so anything that
   * fails here is reported explicitly rather than being retried forever: the
   * attempt cap on the server side would otherwise never be reached and a file
   * the tablet cannot store would be downloaded on every heartbeat.
   *
   * The server hands over a bounded batch at a time, so that an interruption
   * strands a batch rather than a whole folder. Keep asking until it says there
   * is nothing left: waiting for the next heartbeat between batches would turn
   * one folder of tracks into an afternoon of them.
   */
  async poll(c: CloudCredentials): Promise<void> {
    if (this.polling) return;
    this.polling = true;
    try {
      // A cap, not a schedule: this only stops a server that somehow keeps
      // offering the same files from holding this loop open for ever.
      for (let batch = 0; batch < MAX_BATCHES_PER_POLL; batch++) {
        const res = await fetch(`${c.cloudUrl}/api/v1/devices/${c.deviceId}/files`, {
          headers: { Authorization: `Bearer ${c.apiKey}` },
        });
        if (!res.ok) {
          console.warn(`[CloudFiles] Could not list pending files: HTTP ${res.status}`);
          return;
        }
        const pending: PendingFile[] = (await res.json()) ?? [];
        if (pending.length === 0) return;
        for (const f of pending) {
          await this.fetchOne(c, f);
        }
      }
    } catch (error) {
      console.warn('[CloudFiles] Poll failed:', error);
    } finally {
      this.polling = false;
    }
  }

  private async fetchOne(c: CloudCredentials, f: PendingFile): Promise<void> {
    if (!InboxModule?.saveToInbox) {
      await this.report(c, f.name, STATUS_FAILED, 'This build cannot save files to the inbox');
      return;
    }

    // Stage in cache first. Writing straight into the shared folder would leave
    // a truncated file visible to the pupil if the network dropped mid-download.
    const tmpPath = `${RNFS.CachesDirectoryPath}/inbox-${Date.now()}-${sanitize(f.name)}`;
    try {
      const { promise } = RNFS.downloadFile({
        fromUrl: f.download_url,
        toFile: tmpPath,
        headers: { Authorization: `Bearer ${c.apiKey}` },
      });
      const result = await promise;
      if (result.statusCode !== 200) {
        throw new Error(`Download failed: HTTP ${result.statusCode}`);
      }

      const stat = await RNFS.stat(tmpPath);
      const got = Number(stat.size);
      // A size mismatch means a truncated or wrong file; better to fail loudly
      // than to hand a pupil a PDF that will not open.
      if (f.size > 0 && got !== f.size) {
        throw new Error(`Size mismatch: expected ${f.size} bytes, got ${got}`);
      }

      await InboxModule.saveToInbox(
        tmpPath,
        f.file_name || f.name,
        f.content_type ?? '',
        f.rel_path ?? '',
      );
      await this.report(c, f.name, STATUS_DONE, '');
      console.log(`[CloudFiles] Saved ${f.name} to the inbox`);
    } catch (error: any) {
      const message = error?.message ?? String(error);
      console.warn(`[CloudFiles] ${f.name} failed:`, message);
      await this.report(c, f.name, STATUS_FAILED, message);
    } finally {
      // Never leave the staged copy behind: these are whole slide decks.
      try {
        if (await RNFS.exists(tmpPath)) await RNFS.unlink(tmpPath);
      } catch {
        /* a stale cache file is not worth failing the delivery over */
      }
    }
  }

  private async report(
    c: CloudCredentials, name: string, status: string, error: string,
  ): Promise<void> {
    try {
      await fetch(
        `${c.cloudUrl}/api/v1/devices/${c.deviceId}/files/${encodeURIComponent(name)}/result`,
        {
          method: 'POST',
          headers: {
            Authorization: `Bearer ${c.apiKey}`,
            'Content-Type': 'application/json',
          },
          body: JSON.stringify({ status, error }),
        },
      );
    } catch (e) {
      // The server's attempt cap is what stops this being retried forever.
      console.warn(`[CloudFiles] Could not report ${name}:`, e);
    }
  }

  /** What is currently in the inbox. Used by the console's file manager. */
  async list(): Promise<InboxEntry[]> {
    if (!InboxModule?.listInbox) return [];
    try {
      return (await InboxModule.listInbox()) ?? [];
    } catch (error) {
      console.warn('[CloudFiles] Could not list the inbox:', error);
      return [];
    }
  }

  async remove(name: string, relPath = ''): Promise<boolean> {
    if (!InboxModule?.deleteFromInbox) return false;
    try {
      return await InboxModule.deleteFromInbox(name, relPath);
    } catch (error) {
      console.warn(`[CloudFiles] Could not delete ${name}:`, error);
      return false;
    }
  }
}

/** Cache file names are ours, but the display name came off the wire. */
function sanitize(name: string): string {
  return name.replace(/[^a-zA-Z0-9._-]/g, '_').slice(0, 80);
}

export const CloudFileService = new CloudFileServiceImpl();
