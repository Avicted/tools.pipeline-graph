import jetbrains.buildServer.configs.kotlin.*
import jetbrains.buildServer.configs.kotlin.buildSteps.script

version = "2024.03"

project {
    description = "Shared AWS infrastructure (EKS, RDS, networking)"
    buildType(Validate)
    sequential {
        buildType(plan("staging"))
        buildType(apply("staging"))
        buildType(plan("prod"))
        buildType(apply("prod"))
    }
}

object Validate : BuildType({
    name = "terraform validate"
    steps {
        script { scriptContent = "make validate" }
    }
})

fun plan(env: String) = BuildType {
    id("Plan_${env}")
    name = "Plan $env"
    artifactRules = "envs/$env/tfplan"
    steps {
        script { scriptContent = "make plan ENV=$env" }
    }
    dependencies {
        snapshot(Validate) {}
    }
}

fun apply(env: String) = BuildType {
    id("Apply_${env}")
    name = "Apply $env"
    steps {
        script { scriptContent = "make apply ENV=$env" }
    }
    dependencies {
        artifacts(RelativeId("Plan_${env}")) {
            artifactRules = "tfplan => envs/$env/"
        }
    }
}
