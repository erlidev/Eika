/**
 * The one forge link Eika builds: GitHub's compare page, which offers to open
 * a pull request for a branch that was pushed upstream. Eika calls no forge
 * API; a remote that is not on github.com gets no link.
 */

/** githubRepo reads "owner/repo" from a GitHub remote URL, or undefined for any other remote. */
export function githubRepo(remoteUrl: string): string | undefined {
  const url = remoteUrl.trim();
  const patterns = [
    /^https?:\/\/(?:[^@/]+@)?github\.com\/([^/]+)\/([^/]+?)(?:\.git)?\/?$/i,
    /^(?:ssh:\/\/)?git@github\.com[:/]([^/]+)\/([^/]+?)(?:\.git)?\/?$/i,
  ];
  for (const pattern of patterns) {
    const match = pattern.exec(url);
    if (match?.[1] && match[2]) return `${match[1]}/${match[2]}`;
  }
  return undefined;
}

/** compareUrl is the page that opens a pull request for branch, or undefined off GitHub. */
export function compareUrl(remoteUrl: string | undefined, branch: string): string | undefined {
  const repo = githubRepo(remoteUrl ?? "");
  if (repo === undefined || branch === "") return undefined;
  const path = branch.split("/").map(encodeURIComponent).join("/");
  return `https://github.com/${repo}/compare/${path}?expand=1`;
}
