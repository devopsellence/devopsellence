class MarketingController < ApplicationController
  layout "marketing"
  before_action :assign_public_contact_details

  LEGAL_EFFECTIVE_DATE = Date.new(2026, 3, 23)
  CONTACT_EMAIL = "contact@devopsellence.com"

  def index
    assign_public_install_command
  end

  def docs
    assign_public_install_command
  end

  def roadmap
  end

  BLOG_TOPICS = {
    "Architecture" => "How the system is designed and why",
    "Deploy pipeline" => "From git push to running containers",
    "Security" => "Secrets, identity, and TLS",
    "Operations" => "Resilience, scheduling, and day-2 concerns"
  }.freeze

  BLOG_POSTS = [
    {
      slug: "direct-mode-deploy-with-nothing-but-ssh",
      title: "Solo mode: deploy with nothing but SSH",
      description: "No control plane, no registry, no cloud services. Solo mode deploys your app to any server over SSH using docker save/load and a file-based desired state. Same zero-downtime rollouts, same reconciliation loop.",
      date: Date.new(2026, 4, 8),
      reading_time: "9 min read",
      series: nil,
      part: nil,
      topic: "Architecture",
      featured: true
    },
    {
      slug: "standalone-mode-from-gcp-only-to-run-anywhere",
      title: "Standalone mode: from GCP-only to run-anywhere",
      description: "How we rearchitected the runtime layer to make GCP optional, shipped a standalone backend for easy self-hosting, and built the foundation for AWS and multi-provider support.",
      date: Date.new(2026, 4, 6),
      reading_time: "10 min read",
      series: nil,
      part: nil,
      topic: "Architecture"
    },
    {
      slug: "devopsellence-vs-kubernetes-structural-comparison",
      title: "devopsellence vs. Kubernetes: a structural comparison",
      description: "Every Kubernetes component has a devopsellence counterpart. This article maps kube-proxy to Envoy, etcd to GCS, kubelet to the agent, and explains what changes when you strip orchestration to its minimum useful form.",
      date: Date.new(2026, 4, 5),
      reading_time: "10 min read",
      series: nil,
      part: nil,
      topic: "Architecture"
    },
    {
      slug: "gcp-primitives-part-1-the-architecture",
      title: "Why we built VM-native deployment tooling on GCP primitives",
      description: "Most deployment tools centralize everything in one system. We chose a different path — delegating secrets, deploy state, images, signing, and identity to purpose-built GCP services.",
      date: Date.new(2026, 4, 4),
      reading_time: "5 min read",
      series: "Building VM-native deployment tooling on GCP primitives",
      part: 1,
      topic: "Architecture"
    },
    {
      slug: "engineering-decisions-part-4-one-app-per-server",
      title: "One app per server: the power of a deliberate constraint",
      description: "devopsellence assigns one environment per node. This simplifies the agent, eliminates noisy neighbors, and sidesteps the multi-tenant complexity that Kubernetes was built to solve.",
      date: Date.new(2026, 4, 5),
      reading_time: "7 min read",
      series: "Engineering decisions behind devopsellence",
      part: 4,
      topic: "Architecture"
    },
    {
      slug: "engineering-decisions-part-5-docker-not-kubernetes",
      title: "Why Docker and not Kubernetes",
      description: "Kubernetes solves container orchestration at massive scale. devopsellence uses Docker directly for a different problem: deploying apps to your servers without operating a cluster.",
      date: Date.new(2026, 4, 5),
      reading_time: "8 min read",
      series: "Engineering decisions behind devopsellence",
      part: 5,
      topic: "Architecture"
    },
    {
      slug: "gcp-primitives-part-7-whats-next",
      title: "What's next: standalone mode and multi-provider support",
      description: "GCP primitives are the right default, but they shouldn't be the only option. We're making the infrastructure layer pluggable.",
      date: Date.new(2026, 4, 4),
      reading_time: "4 min read",
      series: "Building VM-native deployment tooling on GCP primitives",
      part: 7,
      topic: "Architecture"
    },
    {
      slug: "engineering-decisions-part-1-pull-not-push",
      title: "Pull, don't push: why agents fetch state instead of receiving commands",
      description: "Most deploy tools SSH into servers and run commands. devopsellence agents pull desired state and reconcile locally. Here's why that matters.",
      date: Date.new(2026, 4, 5),
      reading_time: "7 min read",
      series: "Engineering decisions behind devopsellence",
      part: 1,
      topic: "Deploy pipeline"
    },
    {
      slug: "engineering-decisions-part-2-reconciliation-loop",
      title: "The 2-second reconciliation loop",
      description: "Every 2 seconds, the agent fetches desired state, computes a hash diff, and reconciles containers. Inside the loop that makes deploys self-healing.",
      date: Date.new(2026, 4, 5),
      reading_time: "7 min read",
      series: "Engineering decisions behind devopsellence",
      part: 2,
      topic: "Deploy pipeline"
    },
    {
      slug: "engineering-decisions-part-3-zero-downtime",
      title: "Zero-downtime deploys without a central load balancer",
      description: "Every node runs Envoy, configured via xDS. The agent choreographs health checks, EDS updates, and drain delays to swap containers with zero traffic interruption.",
      date: Date.new(2026, 4, 5),
      reading_time: "8 min read",
      series: "Engineering decisions behind devopsellence",
      part: 3,
      topic: "Deploy pipeline"
    },
    {
      slug: "gcp-primitives-part-2-deploy-delivery",
      title: "Tamper-proof deploy delivery with GCS, Cloud KMS, and Artifact Registry",
      description: "How signed envelopes, immutable GCS objects, HSM-backed signing, and per-org image repositories create a deploy pipeline that agents can verify end to end.",
      date: Date.new(2026, 4, 4),
      reading_time: "8 min read",
      series: "Building VM-native deployment tooling on GCP primitives",
      part: 2,
      topic: "Deploy pipeline"
    },
    {
      slug: "gcp-primitives-part-3-secrets",
      title: "Secrets that never touch the database",
      description: "Secret values live in GCP Secret Manager, not in the control plane database. Agents fetch them directly using per-environment IAM credentials.",
      date: Date.new(2026, 4, 4),
      reading_time: "6 min read",
      series: "Building VM-native deployment tooling on GCP primitives",
      part: 3,
      topic: "Security"
    },
    {
      slug: "gcp-primitives-part-4-identity",
      title: "Zero-credential nodes with Workload Identity Federation",
      description: "No long-lived GCP credentials on your servers. A 4-step token exchange chain gives agents short-lived, scoped access to exactly the resources they need.",
      date: Date.new(2026, 4, 4),
      reading_time: "7 min read",
      series: "Building VM-native deployment tooling on GCP primitives",
      part: 4,
      topic: "Security"
    },
    {
      slug: "gcp-primitives-part-5-tls",
      title: "TLS private keys that never leave your servers",
      description: "The agent generates its own RSA key pair. Only the CSR is sent to the control plane. The private key stays on your server, always.",
      date: Date.new(2026, 4, 4),
      reading_time: "5 min read",
      series: "Building VM-native deployment tooling on GCP primitives",
      part: 5,
      topic: "Security"
    },
    {
      slug: "gcp-primitives-part-6-resilience",
      title: "What breaks and what doesn't",
      description: "When the control plane goes down, your apps keep running. Here's exactly what happens when each layer of the architecture fails.",
      date: Date.new(2026, 4, 4),
      reading_time: "5 min read",
      series: "Building VM-native deployment tooling on GCP primitives",
      part: 6,
      topic: "Operations"
    },
    {
      slug: "engineering-decisions-part-6-scheduling",
      title: "Label-based scheduling and the warm pool model",
      description: "Node labels, capabilities, explicit assignment, and pre-provisioned identity bundles. How devopsellence places workloads without a scheduler.",
      date: Date.new(2026, 4, 5),
      reading_time: "7 min read",
      series: "Engineering decisions behind devopsellence",
      part: 6,
      topic: "Operations"
    }
  ].freeze

  BLOG_SERIES = BLOG_POSTS
    .select { |p| p[:series] }
    .group_by { |p| p[:series] }
    .transform_values { |posts| posts.sort_by { |p| p[:part] } }
    .freeze

  def blog_index
    @featured = BLOG_POSTS.find { |p| p[:featured] }
    @topics = BLOG_TOPICS
    @posts_by_topic = BLOG_POSTS.reject { |p| p[:featured] }.group_by { |p| p[:topic] }
    @series = BLOG_SERIES
  end

  def blog_show
    @post = BLOG_POSTS.find { |p| p[:slug] == params[:slug] }
    raise ActionController::RoutingError, "Not Found" unless @post

    render "marketing/blog/#{@post[:slug]}"
  end

  def privacy
    assign_legal_page
  end

  def terms
    assign_legal_page
  end

  private
    def assign_public_contact_details
      @contact_email = CONTACT_EMAIL
    end

    def assign_public_install_command
      @cli_install_base_url = request.base_url
      @cli_install_command = "curl -fsSL #{@cli_install_base_url}/lfg.sh | bash"
      @agent_uninstall_command = "curl -fsSL #{@cli_install_base_url}/uninstall.sh | bash -s -- --purge-runtime"
    end

    def assign_legal_page
      @legal_effective_date = LEGAL_EFFECTIVE_DATE.strftime("%B %-d, %Y")
    end
end
