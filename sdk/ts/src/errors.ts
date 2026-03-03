/**
 * Typed HTTP error for agently-core API calls.
 */
export class HttpError extends Error {
    status: number;
    statusText: string;
    body: string;

    constructor(status: number, statusText: string, body: string) {
        super(`HTTP ${status} ${statusText}: ${body}`);
        this.name = 'HttpError';
        this.status = status;
        this.statusText = statusText;
        this.body = body;
    }
}
