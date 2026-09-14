import { describe, expect, it } from "vitest";

import { validate } from "@/features/projects/validate";

describe("validate", () => {
  it("accepts a well-formed local project", () => {
    expect(
      validate({
        name: "eika",
        kind: "local",
        host_path: "/home/you/code",
        default_branch: "main",
      }),
    ).toBeNull();
  });

  it("accepts a well-formed remote project", () => {
    expect(
      validate({
        name: "eika",
        kind: "remote",
        remote_url: "https://example.test/repo.git",
        default_branch: "main",
      }),
    ).toBeNull();
  });

  it("rejects a name that would escape the hub directory", () => {
    expect(validate({ name: "a/b", kind: "local", host_path: "/x" })).toMatch(/slash/);
    expect(validate({ name: "..", kind: "local", host_path: "/x" })).toMatch(/slash/);
  });

  it("rejects a missing name", () => {
    expect(validate({ name: "  ", kind: "local", host_path: "/x" })).toMatch(/name is required/);
  });

  it("rejects a remote project with no URL", () => {
    expect(validate({ name: "a", kind: "remote", remote_url: "" })).toMatch(/remote project/);
  });

  it("rejects a remote URL that could carry a credential", () => {
    expect(validate({ name: "a", kind: "remote", remote_url: "https://u:p@host/r.git" })).toMatch(
      /userinfo/,
    );
    expect(validate({ name: "a", kind: "remote", remote_url: "https://host/r.git?x=1" })).toMatch(
      /userinfo/,
    );
  });

  it("rejects half a credential pair", () => {
    expect(
      validate({
        name: "a",
        kind: "remote",
        remote_url: "https://host/r.git",
        remote_username_env: "EIKA_GIT_USERNAME",
      }),
    ).toMatch(/both credential variables/);
  });

  it("accepts a whole credential pair", () => {
    expect(
      validate({
        name: "a",
        kind: "remote",
        remote_url: "https://host/r.git",
        remote_username_env: "EIKA_GIT_USERNAME",
        remote_password_env: "EIKA_GIT_PASSWORD",
      }),
    ).toBeNull();
  });

  it("rejects a local project whose path is not absolute", () => {
    expect(validate({ name: "a", kind: "local", host_path: "code" })).toMatch(/absolute path/);
  });
});
