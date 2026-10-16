import jetbrains.buildServer.configs.kotlin.*
import jetbrains.buildServer.configs.kotlin.buildSteps.*
import jetbrains.buildServer.configs.kotlin.triggers.*

version = "2024.03"

project {
    description = "Customer web app"
    buildType(Build)
    buildType(Storybook)
    buildType(E2E)
    buildType(PublishCdn)
}

object Build : BuildType({
    name = "Build web app"
    artifactRules = "app/dist/** => web-dist.zip"
    steps {
        exec {
            name = "bundle"
            path = "make"
            arguments = "-C app bundle"
        }
    }
    dependencies {
        snapshot(AbsoluteId("PlatformImages_BaseNode")) {}
    }
    triggers {
        vcs { branchFilter = "+:*" }
    }
})

object Storybook : BuildType({
    name = "Storybook"
    steps {
        script { scriptContent = "make -C app storybook" }
    }
    dependencies {
        snapshot(Build) {}
    }
})

object E2E : BuildType({
    name = "E2E (Playwright)"
    steps {
        script {
            scriptContent = """
                docker run -d --name orders registry.acme.io/orders:latest
                docker run -d --name payments registry.acme.io/payments:latest
                make -C e2e run
            """.trimIndent()
        }
    }
    dependencies {
        artifacts(Build) { artifactRules = "web-dist.zip!** => app/dist" }
        snapshot(AbsoluteId("OrdersService_Image")) {}
        snapshot(AbsoluteId("PaymentsService_Image")) {}
    }
})

object PublishCdn : BuildType({
    name = "Publish to CDN"
    steps {
        script { scriptContent = "make -C app publish BUCKET=s3://acme-web-%teamcity.build.branch%" }
    }
    dependencies {
        dependency(Build) {
            snapshot {}
            artifacts { artifactRules = "web-dist.zip!** => app/dist" }
        }
        snapshot(E2E) {}
    }
    triggers {
        finishBuildTrigger {
            buildType = "${E2E.id}"
            successfulOnly = true
            branchFilter = "+:<default>"
        }
    }
})
