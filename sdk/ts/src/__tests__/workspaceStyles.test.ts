import {describe, it, expect, vi} from 'vitest';
import {AgentlyClient} from '../client';
const revision = 'a'.repeat(64);
describe('workspace style assets', () => {
    it('preserves metadata descriptors and uses configured asset authentication', async () => {
        const metadata = {workspaceId: 'workspace', uiStyles: {version: 1, revision, href: `/v1/workspace/ui/styles/${revision}.css`}};
        const fetchImpl = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify({data: metadata}), {headers: {'content-type': 'application/json'}}))
            .mockResolvedValueOnce(new Response('.agently-workspace {color:red}', {headers: {'content-type': 'text/css'}}));
        const client = new AgentlyClient({baseURL: 'https://backend.example/v1', tokenProvider: async () => 'test-token', useCookies: true, fetchImpl});
        expect(await client.getWorkspaceMetadata()).toMatchObject(metadata);
        expect(await client.getWorkspaceStyleAsset(metadata.uiStyles.href)).toContain('color:red');
        expect(fetchImpl.mock.calls[1][0]).toBe(`https://backend.example/v1/workspace/ui/styles/${revision}.css`);
        expect(fetchImpl.mock.calls[1][1]).toMatchObject({credentials: 'include', headers: {Authorization: 'Bearer test-token', Accept: 'text/css'}});
    });
    it('rejects arbitrary URLs before sending credentials', async () => {
        const fetchImpl = vi.fn();
        const client = new AgentlyClient({baseURL: '/v1', fetchImpl});
        await expect(client.getWorkspaceStyleAsset('https://other.example/style.css')).rejects.toThrow();
        expect(fetchImpl).not.toHaveBeenCalled();
    });
});
