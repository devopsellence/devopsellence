import { defineConfig } from "astro/config";
import starlight from "@astrojs/starlight";

export default defineConfig({
  site: "https://docs.devopsellence.com",
  integrations: [
    starlight({
      title: "devopsellence docs",
      description: "Deploy and operate containerized apps on VMs with devopsellence.",
      editLink: {
        baseUrl: "https://github.com/devopsellence/devopsellence/edit/master/docs-website/",
      },
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/devopsellence/devopsellence",
        },
      ],
      customCss: ["./src/styles/custom.css"],
      sidebar: [
        {
          label: "Start",
          items: [
            { label: "Overview", link: "/getting-started/overview/" },
            { label: "Install", link: "/getting-started/install/" },
            { label: "Solo quickstart", link: "/getting-started/solo-quickstart/" },
            { label: "Shared overview", link: "/getting-started/shared-overview/" },
          ],
        },
        {
          label: "Core concepts",
          items: [
            { label: "Runtime model", link: "/concepts/runtime-model/" },
            { label: "Solo and shared", link: "/concepts/solo-and-shared/" },
            { label: "Node agent and desired state", link: "/concepts/agent-desired-state/" },
            { label: "AI operator model", link: "/concepts/agent-primary/" },
          ],
        },
        {
          label: "Guides",
          items: [
            { label: "Deploy with solo", link: "/guides/solo-deploy/" },
            { label: "Deploy with shared", link: "/guides/shared-deploy/" },
            { label: "GitHub Actions", link: "/guides/github-actions/" },
            { label: "Secrets", link: "/guides/secrets/" },
            { label: "Ingress and TLS", link: "/guides/ingress-tls/" },
            { label: "Rollback", link: "/guides/rollback/" },
            { label: "Troubleshooting", link: "/guides/troubleshooting/" },
            { label: "Cleanup", link: "/guides/cleanup/" },
          ],
        },
        {
          label: "Examples",
          items: [
            { label: "Basecamp Fizzy on Rails", link: "/examples/fizzy-rails-solo/" },
          ],
        },
        {
          label: "Reference",
          items: [
            { label: "devopsellence.yml", link: "/reference/devopsellence-yml/" },
            { label: "CLI commands", link: "/reference/cli/" },
            { label: "Result JSON", link: "/reference/result-json/" },
            { label: "Environment variables", link: "/reference/environment-variables/" },
          ],
        },
        {
          label: "Operate this site",
          items: [
            { label: "Docs website component", link: "/operate/docs-website/" },
          ],
        },
      ],
    }),
  ],
});
