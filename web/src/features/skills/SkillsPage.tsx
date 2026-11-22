/** The Skills page: the installed skills with a zip install, and the open skill beside them. */

import { useParams } from "react-router";

import { MasterDetail } from "@/components/MasterDetail";
import { SkillDetail } from "@/features/skills/SkillDetail";
import { SkillList } from "@/features/skills/SkillList";

export function SkillsPage() {
  const { name } = useParams();
  return (
    <MasterDetail
      listLabel="Skills"
      open={name !== undefined}
      list={<SkillList />}
      detail={
        name === undefined ? (
          <p className="text-muted-foreground m-auto p-4 text-sm">Pick a skill to read it.</p>
        ) : (
          <SkillDetail key={name} name={name} />
        )
      }
    />
  );
}
