import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";
import type { Job } from "../types";
export function cn(...inputs: ClassValue[]) { return twMerge(clsx(inputs)); }
export function taskPercent(job: Job) {
  const scanning = job.scan?.phase !== "indexed" && job.current.some(activity => ["storage", "discover", "scan", "read", "staging_wait", "probe", "artwork"].includes(activity.phase));
  return Math.round(Math.max(0, Math.min(1, scanning && job.scan?.total ? job.scan.processed / job.scan.total : job.progress)) * 100);
}
