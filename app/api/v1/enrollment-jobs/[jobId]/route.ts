import { getDatabase } from "@/lib/server/db";
import { jsonError } from "@/lib/server/http";
import { requireApiSession } from "@/lib/server/session";

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

export async function GET(request: Request, { params }: { params: Promise<{ jobId: string }> }) {
  const session = await requireApiSession(request);
  if (session instanceof Response) return session;
  const { jobId } = await params;
  const { sqlite } = getDatabase();
  const job = sqlite
    .prepare(
      "SELECT id, system_id AS systemId, status, stage, error_code AS errorCode, error_message AS errorMessage, attempt, created_at AS createdAt, updated_at AS updatedAt FROM enrollment_job WHERE id = ?",
    )
    .get(jobId) as
    | {
        id: string;
        systemId: string;
        status: string;
        stage: string;
        errorCode: string | null;
        errorMessage: string | null;
        attempt: number;
        createdAt: number;
        updatedAt: number;
      }
    | undefined;
  if (!job) return jsonError("Enrollment job not found.", 404);
  return Response.json(
    {
      ...job,
      createdAt: new Date(job.createdAt).toISOString(),
      updatedAt: new Date(job.updatedAt).toISOString(),
    },
    { headers: { "Cache-Control": "no-store" } },
  );
}
